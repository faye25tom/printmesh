package netnode

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/tailscale"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

type Config struct {
	Hostname   string
	ControlURL string
	AuthKey    string
	StateDir   string
}

type StatusSnapshot struct {
	State   string
	AuthURL string
	IP      string
}

type Node struct {
	Srv *tsnet.Server
	LC  *tailscale.LocalClient

	mu      sync.RWMutex
	state   string
	authURL string
	ip      string
}

type PrintHost struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
}

// Start 阻塞式启动节点（适合 CLI 命令行工具）
func Start(ctx context.Context, cfg Config) (*Node, error) {
	n, err := StartAsync(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := n.Srv.Up(ctx); err != nil {
		_ = n.Srv.Close()
		return nil, err
	}
	return n, nil
}

// StartAsync 非阻塞启动节点并通过 WatchIPNBus 实时捕获登录状态与 AuthURL（适合 Windows Service）
func StartAsync(ctx context.Context, cfg Config) (*Node, error) {
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, err
	}

	srv := &tsnet.Server{
		Hostname:   cfg.Hostname,
		ControlURL: cfg.ControlURL,
		AuthKey:    cfg.AuthKey,
		Dir:        filepath.Join(cfg.StateDir, cfg.Hostname),
		Logf:       func(format string, args ...any) {},
		UserLogf:   log.Printf,
	}

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("tsnet start failed: %w", err)
	}

	lc, err := srv.LocalClient()
	if err != nil {
		_ = srv.Close()
		return nil, err
	}

	n := &Node{
		Srv:   srv,
		LC:    lc,
		state: "Starting",
	}

	go n.watchBus(ctx)
	return n, nil
}

func (n *Node) watchBus(ctx context.Context) {
	watcher, err := n.LC.WatchIPNBus(ctx, ipn.NotifyInitialState)
	if err != nil {
		log.Printf("[tsnet] WatchIPNBus error: %v", err)
		return
	}
	defer watcher.Close()

	for {
		notify, err := watcher.Next()
		if err != nil {
			return
		}
		n.mu.Lock()
		if notify.State != nil {
			n.state = notify.State.String()
			if *notify.State == ipn.Running {
				n.authURL = ""
				if st, err := n.LC.Status(ctx); err == nil && len(st.TailscaleIPs) > 0 {
					n.ip = st.TailscaleIPs[0].String()
				}
			}
		}
		if notify.BrowseToURL != nil && *notify.BrowseToURL != "" {
			n.authURL = *notify.BrowseToURL
			n.state = "NeedsLogin"
		}
		n.mu.Unlock()
	}
}

func (n *Node) Snapshot() StatusSnapshot {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return StatusSnapshot{
		State:   n.state,
		AuthURL: n.authURL,
		IP:      n.ip,
	}
}

func (n *Node) StartLoginInteractive(ctx context.Context) error {
	return n.LC.StartLoginInteractive(ctx)
}

func (n *Node) Logout(ctx context.Context) error {
	return n.LC.Logout(ctx)
}

func (n *Node) NewHTTPClient() *http.Client {
	cli := n.Srv.HTTPClient()
	cli.Timeout = 5 * time.Minute
	return cli
}

func (n *Node) DiscoverPrintHosts(ctx context.Context) ([]PrintHost, error) {
	st, err := n.LC.Status(ctx)
	if err != nil {
		return nil, err
	}

	var hosts []PrintHost
	for _, peer := range st.Peer {
		if peer.Online && strings.HasPrefix(peer.HostName, "pm-srv-") && len(peer.TailscaleIPs) > 0 {
			hosts = append(hosts, PrintHost{
				Hostname: peer.HostName,
				IP:       peer.TailscaleIPs[0].String(),
				Online:   true,
			})
		}
	}
	return hosts, nil
}

func (n *Node) Close() error {
	return n.Srv.Close()
}
