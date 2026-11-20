//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"printmesh/internal/client"
	"printmesh/internal/ippserver"
	"printmesh/internal/netnode"
)

func main() {
	hostname := flag.String("hostname", "pm-cli-laptop", "本机节点名")
	controlURL := flag.String("control-url", "", "Headscale 地址（留空则使用官方 Tailscale）")
	authKey := flag.String("auth-key", "", "预授权密钥（可选）")
	remoteHost := flag.String("remote-host", "pm-srv-office", "远端打印服务端节点名或 Tailscale IP")
	remotePrinter := flag.String("remote-printer", "", "远端共享的物理打印机名称")
	mode := flag.String("mode", "ipp", "连接模式: ipp (免驱) 或 raw (原厂驱动)")
	localPort := flag.Int("port", 19632, "本地代理监听端口")
	flag.Parse()

	if *remotePrinter == "" {
		log.Fatal("请通过 -remote-printer 指定远端打印机名称")
	}

	ctx := context.Background()
	node, err := netnode.Start(ctx, netnode.Config{
		Hostname:   *hostname,
		ControlURL: *controlURL,
		AuthKey:    *authKey,
		StateDir:   "./state",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()

	tsHTTP := node.NewHTTPClient()
	virtualName := fmt.Sprintf("%s (%s)", *remotePrinter, *remoteHost)

	if *mode == "raw" {
		rawEndpoint := fmt.Sprintf("http://%s:19631/api/print-raw?printer=%s",
			*remoteHost, url.QueryEscape(*remotePrinter))
		go func() {
			if err := client.StartRawBridge(*localPort, tsHTTP, rawEndpoint); err != nil {
				log.Fatal(err)
			}
		}()
		time.Sleep(300 * time.Millisecond)

		if err := client.InstallRemoteRawPrinter(tsHTTP, *remoteHost, *remotePrinter, virtualName, *localPort); err != nil {
			log.Fatalf("自动安装原厂驱动并绑定打印机失败: %v", err)
		}
		log.Printf("[Client] 已通过原厂驱动模式注册打印机 %q", virtualName)
		select {}
	}

	uploadEndpoint := fmt.Sprintf("http://%s:19631/api/print?printer=%s",
		*remoteHost, url.QueryEscape(*remotePrinter))
	listenAddr := fmt.Sprintf("127.0.0.1:%d", *localPort)
	ippURL := fmt.Sprintf("http://%s/ipp/print", listenAddr)

	handler := &ippserver.Handler{
		PrinterURI: ippURL,
		OnPDFReady: func(pdfPath string) error {
			defer os.Remove(pdfPath)
			f, err := os.Open(pdfPath)
			if err != nil {
				return err
			}
			defer f.Close()

			req, err := http.NewRequest(http.MethodPost, uploadEndpoint, f)
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/pdf")

			resp, err := tsHTTP.Do(req)
			if err != nil {
				return fmt.Errorf("tsnet post failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("remote print error (%d): %s", resp.StatusCode, string(body))
			}
			return nil
		},
	}

	go func() {
		if err := http.ListenAndServe(listenAddr, handler); err != nil {
			log.Fatal(err)
		}
	}()

	time.Sleep(500 * time.Millisecond)
	if err := client.RegisterIPPPrinter(virtualName, ippURL); err != nil {
		log.Fatalf("注册虚拟打印机失败: %v", err)
	}
	log.Printf("[Client] 已注册免驱远程打印机 %q", virtualName)
	select {}
}
