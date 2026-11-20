//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows/svc"

	"printmesh/internal/client"
	"printmesh/internal/ippserver"
	"printmesh/internal/netnode"
	"printmesh/internal/server"
)

const (
	ServiceName = "PrintMeshDaemon"
	PipeName    = `\\.\pipe\printmesh-ipc`
	PipeSDDL    = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"
)

type Config struct {
	Hostname       string   `json:"hostname"`
	ControlURL     string   `json:"control_url"`
	AuthKey        string   `json:"auth_key"`
	SharedPrinters []string `json:"shared_printers"`
}

type Engine struct {
	mu       sync.RWMutex
	dataDir  string
	cfg      Config
	node     *netnode.Node
	cancel   context.CancelFunc
	nextPort atomic.Int32
}

func NewEngine() *Engine {
	progData := os.Getenv("ProgramData")
	if progData == "" {
		progData = `C:\ProgramData`
	}
	dir := filepath.Join(progData, "PrintMesh")
	_ = os.MkdirAll(dir, 0755)

	e := &Engine{
		dataDir: dir,
		cfg: Config{
			Hostname: "pm-node",
		},
	}
	e.nextPort.Store(19632)
	e.loadConfig()
	return e
}

func (e *Engine) configPath() string {
	return filepath.Join(e.dataDir, "config.json")
}

func (e *Engine) loadConfig() {
	b, err := os.ReadFile(e.configPath())
	if err != nil {
		return
	}
	var c Config
	if json.Unmarshal(b, &c) == nil && c.Hostname != "" {
		e.cfg = c
	}
}

func (e *Engine) SaveConfigAndReload(parentCtx context.Context, c Config) error {
	e.mu.Lock()
	needRestart := e.cfg.Hostname != c.Hostname || e.cfg.ControlURL != c.ControlURL || (c.AuthKey != "" && e.cfg.AuthKey != c.AuthKey)
	e.cfg = c
	e.mu.Unlock()

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(e.configPath(), b, 0600); err != nil {
		return err
	}
	if needRestart {
		go e.Start(parentCtx)
	}
	return nil
}

func (e *Engine) Start(parentCtx context.Context) {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	if e.node != nil {
		_ = e.node.Close()
		e.node = nil
	}
	ctx, cancel := context.WithCancel(parentCtx)
	e.cancel = cancel
	cfg := e.cfg
	e.mu.Unlock()

	node, err := netnode.StartAsync(ctx, netnode.Config{
		Hostname:   cfg.Hostname,
		ControlURL: cfg.ControlURL,
		AuthKey:    cfg.AuthKey,
		StateDir:   filepath.Join(e.dataDir, "state"),
	})
	if err != nil {
		log.Printf("[Engine] tsnet start error: %v", err)
		return
	}

	e.mu.Lock()
	e.node = node
	e.mu.Unlock()

	go e.servePrintAPI(node)
}

func (e *Engine) servePrintAPI(node *netnode.Node) {
	mux := http.NewServeMux()
	isAllowed := func(name string) bool {
		e.mu.RLock()
		defer e.mu.RUnlock()
		for _, p := range e.cfg.SharedPrinters {
			if p == name {
				return true
			}
		}
		return false
	}

	mux.HandleFunc("GET /api/printers", func(w http.ResponseWriter, r *http.Request) {
		e.mu.RLock()
		host := e.cfg.Hostname
		printers := append([]string(nil), e.cfg.SharedPrinters...)
		e.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"host": host, "printers": printers})
	})

	mux.HandleFunc("POST /api/print", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("printer")
		if !isAllowed(p) {
			http.Error(w, "printer not shared", http.StatusForbidden)
			return
		}
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("pm-srv-%d.pdf", time.Now().UnixNano()))
		f, err := os.Create(tmp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(f, io.LimitReader(r.Body, 200<<20))
		f.Close()
		defer os.Remove(tmp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := server.PrintPDFToWindowsPrinter(tmp, p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/print-raw", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("printer")
		if !isAllowed(p) {
			http.Error(w, "printer not shared", http.StatusForbidden)
			return
		}
		if err := server.PrintRawStreamToWindowsPrinter(p, "PrintMesh RAW Job", r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	cacheDir := filepath.Join(e.dataDir, "driver-cache")
	manualDir := filepath.Join(e.dataDir, "drivers")

	mux.HandleFunc("GET /api/driver/meta", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("printer")
		if !isAllowed(p) {
			http.Error(w, "printer not shared", http.StatusForbidden)
			return
		}
		meta, err := server.PrepareDriverPackage(p, cacheDir, manualDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	})

	mux.HandleFunc("GET /api/driver/download", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("printer")
		if !isAllowed(p) {
			http.Error(w, "printer not shared", http.StatusForbidden)
			return
		}
		meta, err := server.PrepareDriverPackage(p, cacheDir, manualDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, meta.ZipPath)
	})

	ln, err := node.Srv.Listen("tcp", ":19631")
	if err != nil {
		log.Printf("[Engine] tsnet listen failed: %v", err)
		return
	}
	_ = http.Serve(ln, mux)
}

type printMeshService struct{}

func (m *printMeshService) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := NewEngine()
	ipcServer, err := startIPCServer(ctx, eng)
	if err != nil {
		log.Printf("IPC 启动失败: %v", err)
		return false, 1
	}

	go eng.Start(ctx)
	s <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			cancel()
			shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			_ = ipcServer.Shutdown(shutdownCtx)
			done()
			return false, 0
		}
	}
	return false, 0
}

func startIPCServer(rootCtx context.Context, eng *Engine) (*http.Server, error) {
	ln, err := winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /ipc/status", func(w http.ResponseWriter, r *http.Request) {
		eng.mu.RLock()
		node := eng.node
		cfg := eng.cfg
		eng.mu.RUnlock()

		snap := netnode.StatusSnapshot{State: "Starting"}
		if node != nil {
			snap = node.Snapshot()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":         Version,
			"state":           snap.State,
			"auth_url":        snap.AuthURL,
			"ip":              snap.IP,
			"hostname":        cfg.Hostname,
			"control_url":     cfg.ControlURL,
			"shared_printers": cfg.SharedPrinters,
		})
	})

	mux.HandleFunc("POST /ipc/login", func(w http.ResponseWriter, r *http.Request) {
		eng.mu.RLock()
		node := eng.node
		eng.mu.RUnlock()
		if node == nil {
			http.Error(w, "节点尚未启动", http.StatusServiceUnavailable)
			return
		}
		if err := node.StartLoginInteractive(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /ipc/logout", func(w http.ResponseWriter, r *http.Request) {
		eng.mu.RLock()
		node := eng.node
		eng.mu.RUnlock()
		if node == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := node.Logout(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /ipc/local-printers", func(w http.ResponseWriter, r *http.Request) {
		list, err := server.ListLocalPrinters()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("GET /ipc/remote-printers", func(w http.ResponseWriter, r *http.Request) {
		eng.mu.RLock()
		node := eng.node
		eng.mu.RUnlock()
		if node == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		hosts, err := node.DiscoverPrintHosts(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tsHTTP := node.NewHTTPClient()
		tsHTTP.Timeout = 5 * time.Second

		var results []map[string]any
		for _, h := range hosts {
			resp, err := tsHTTP.Get(fmt.Sprintf("http://%s:19631/api/printers", h.IP))
			if err != nil {
				continue
			}
			var payload struct {
				Host     string   `json:"host"`
				Printers []string `json:"printers"`
			}
			if json.NewDecoder(resp.Body).Decode(&payload) == nil {
				for _, p := range payload.Printers {
					results = append(results, map[string]any{
						"host":    h.Hostname,
						"ip":      h.IP,
						"printer": p,
					})
				}
			}
			resp.Body.Close()
		}
		if results == nil {
			results = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	})

	mux.HandleFunc("POST /ipc/config", func(w http.ResponseWriter, r *http.Request) {
		var cfg Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := eng.SaveConfigAndReload(rootCtx, cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /ipc/connect-printer", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RemoteHost    string `json:"remote_host"`
			RemotePrinter string `json:"remote_printer"`
			Mode          string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		eng.mu.RLock()
		node := eng.node
		eng.mu.RUnlock()
		if node == nil {
			http.Error(w, "tsnet 尚未就绪", http.StatusServiceUnavailable)
			return
		}

		port := int(eng.nextPort.Add(1))
		tsHTTP := node.NewHTTPClient()
		displayName := fmt.Sprintf("%s (%s)", req.RemotePrinter, req.RemoteHost)

		if req.Mode == "raw" {
			rawEndpoint := fmt.Sprintf("http://%s:19631/api/print-raw?printer=%s",
				req.RemoteHost, url.QueryEscape(req.RemotePrinter))
			go client.StartRawBridge(port, tsHTTP, rawEndpoint)
			time.Sleep(200 * time.Millisecond)
			if err := client.InstallRemoteRawPrinter(tsHTTP, req.RemoteHost, req.RemotePrinter, displayName, port); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			uploadEndpoint := fmt.Sprintf("http://%s:19631/api/print?printer=%s",
				req.RemoteHost, url.QueryEscape(req.RemotePrinter))
			listenAddr := fmt.Sprintf("127.0.0.1:%d", port)
			ippURL := fmt.Sprintf("http://%s/ipp/print", listenAddr)

			h := &ippserver.Handler{
				PrinterURI: ippURL,
				OnPDFReady: func(pdfPath string) error {
					defer os.Remove(pdfPath)
					f, err := os.Open(pdfPath)
					if err != nil {
						return err
					}
					defer f.Close()
					httpReq, _ := http.NewRequest(http.MethodPost, uploadEndpoint, f)
					httpReq.Header.Set("Content-Type", "application/pdf")
					resp, err := tsHTTP.Do(httpReq)
					if err != nil {
						return err
					}
					resp.Body.Close()
					return nil
				},
			}
			go http.ListenAndServe(listenAddr, h)
			time.Sleep(300 * time.Millisecond)
			if err := client.RegisterIPPPrinter(displayName, ippURL); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("IPC serve error: %v", err)
		}
	}()
	return srv, nil
}
