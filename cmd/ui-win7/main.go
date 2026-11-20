//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"printmesh/frontend"
)

const PipeName = `\\.\pipe\printmesh-ipc`

var Version = "dev"

// Win7 零 WebView2 轻量控制台：
// 无需联网下载 Edge WebView2 Runtime，启动本地随机端口 HTTP 服务（带会话 Token 校验），
// 自动唤起系统已有的浏览器（Chrome / Edge / Firefox / 360 等）访问与 Wails 完全一致的前端界面。
func main() {
	startMinimized := false
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, "--minimized") {
			startMinimized = true
			break
		}
	}

	token := randomToken()
	ipcClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				timeout := 3 * time.Second
				return winio.DialPipe(PipeName, &timeout)
			},
		},
		Timeout: 90 * time.Second,
	}

	mux := http.NewServeMux()
	fileSrv := http.FileServer(http.FS(frontend.DistFS()))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.URL.Query().Get("token") != token {
			http.Error(w, "Unauthorized: 请通过 PrintMesh 桌面快捷方式打开控制台", http.StatusForbidden)
			return
		}
		fileSrv.ServeHTTP(w, r)
	})

	// 将前端发来的 /ui-api/* 请求安全转发至后台服务的 Named Pipe
	mux.HandleFunc("/ui-api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PrintMesh-Token") != token {
			http.Error(w, "invalid session token", http.StatusForbidden)
			return
		}
		ipcPath := "/ipc/" + strings.TrimPrefix(r.URL.Path, "/ui-api/")
		req, err := http.NewRequestWithContext(r.Context(), r.Method, "http://ipc"+ipcPath, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		}

		resp, err := ipcClient.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("无法连接后台服务: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	consoleURL := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), token)
	log.Printf("[PrintMesh Win7 UI %s] 本地控制台已就绪: %s", Version, consoleURL)

	if !startMinimized {
		go func() {
			time.Sleep(250 * time.Millisecond)
			openBrowser(consoleURL)
		}()
	}

	log.Fatal(http.Serve(ln, mux))
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func openBrowser(rawURL string) {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", rawURL).Start()
}
