//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"printmesh/internal/netnode"
	"printmesh/internal/server"
)

func main() {
	hostname := flag.String("hostname", "pm-srv-office", "节点名称")
	controlURL := flag.String("control-url", "", "Headscale 地址（留空则使用官方 Tailscale）")
	authKey := flag.String("auth-key", "", "预授权密钥（可选）")
	printersFlag := flag.String("printers", "", "允许共享的本机打印机名称，多个用英文逗号分隔")
	flag.Parse()

	if *printersFlag == "" {
		log.Fatal("请通过 -printers 指定至少一台本机物理打印机名称")
	}

	allowed := make(map[string]bool)
	var printerList []string
	for _, p := range strings.Split(*printersFlag, ",") {
		name := strings.TrimSpace(p)
		if name != "" {
			allowed[name] = true
			printerList = append(printerList, name)
		}
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/printers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"host": *hostname, "printers": printerList})
	})

	mux.HandleFunc("POST /api/print", func(w http.ResponseWriter, r *http.Request) {
		targetPrinter := r.URL.Query().Get("printer")
		if !allowed[targetPrinter] {
			http.Error(w, "printer not shared", http.StatusForbidden)
			return
		}
		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("pm-srv-%d.pdf", time.Now().UnixNano()))
		f, err := os.Create(tmpPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written, copyErr := io.Copy(f, io.LimitReader(r.Body, 200<<20))
		f.Close()
		defer os.Remove(tmpPath)
		if copyErr != nil || written == 0 {
			http.Error(w, "failed to receive pdf body", http.StatusBadRequest)
			return
		}
		if err := server.PrintPDFToWindowsPrinter(tmpPath, targetPrinter); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /api/print-raw", func(w http.ResponseWriter, r *http.Request) {
		targetPrinter := r.URL.Query().Get("printer")
		if !allowed[targetPrinter] {
			http.Error(w, "printer not allowed", http.StatusForbidden)
			return
		}
		if err := server.PrintRawStreamToWindowsPrinter(targetPrinter, "PrintMesh RAW Job", r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /api/driver/meta", func(w http.ResponseWriter, r *http.Request) {
		printer := r.URL.Query().Get("printer")
		if !allowed[printer] {
			http.Error(w, "printer not allowed", http.StatusForbidden)
			return
		}
		meta, err := server.PrepareDriverPackage(printer, "./driver-cache", "./drivers")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	})

	mux.HandleFunc("GET /api/driver/download", func(w http.ResponseWriter, r *http.Request) {
		printer := r.URL.Query().Get("printer")
		if !allowed[printer] {
			http.Error(w, "printer not allowed", http.StatusForbidden)
			return
		}
		meta, err := server.PrepareDriverPackage(printer, "./driver-cache", "./drivers")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, meta.ZipPath)
	})

	ln, err := node.Srv.Listen("tcp", ":19631")
	if err != nil {
		log.Fatalf("tsnet listen failed: %v", err)
	}
	log.Printf("[Server] 打印服务已在组网内监听 :19631，共享打印机: %v", printerList)
	log.Fatal(http.Serve(ln, mux))
}
