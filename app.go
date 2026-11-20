//go:build windows && !win7

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const PipeName = `\\.\pipe\printmesh-ipc`

type App struct {
	ctx     context.Context
	version string
	ipcCli  *http.Client
}

type DaemonConfig struct {
	Hostname       string   `json:"hostname"`
	ControlURL     string   `json:"control_url"`
	AuthKey        string   `json:"auth_key"`
	SharedPrinters []string `json:"shared_printers"`
}

func NewApp(version string) *App {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			timeout := 3 * time.Second
			return winio.DialPipe(PipeName, &timeout)
		},
	}
	return &App{
		version: version,
		ipcCli: &http.Client{
			Transport: transport,
			Timeout:   90 * time.Second,
		},
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) GetVersion() string {
	return a.version
}

func (a *App) GetDaemonStatus() (map[string]any, error) {
	resp, err := a.ipcCli.Get("http://ipc/ipc/status")
	if err != nil {
		return nil, fmt.Errorf("无法连接到后台服务，请确认 PrintMesh 服务已启动: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *App) ListLocalPrinters() ([]string, error) {
	resp, err := a.ipcCli.Get("http://ipc/ipc/local-printers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取本机打印机失败: %s", string(body))
	}
	var printers []string
	if err := json.NewDecoder(resp.Body).Decode(&printers); err != nil {
		return nil, err
	}
	return printers, nil
}

func (a *App) ListRemotePrinters() ([]map[string]any, error) {
	resp, err := a.ipcCli.Get("http://ipc/ipc/remote-printers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("扫描远程打印机失败: %s", string(body))
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (a *App) SaveConfig(cfg DaemonConfig) error {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	resp, err := a.ipcCli.Post("http://ipc/ipc/config", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("保存配置失败: %s", string(body))
	}
	return nil
}

func (a *App) StartLogin() error {
	resp, err := a.ipcCli.Post("http://ipc/ipc/login", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("触发登录失败: %s", string(body))
	}
	return nil
}

func (a *App) Logout() error {
	resp, err := a.ipcCli.Post("http://ipc/ipc/logout", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("退出登录失败: %s", string(body))
	}
	return nil
}

func (a *App) ConnectRemotePrinter(remoteHost, remotePrinter, mode string) error {
	payload, _ := json.Marshal(map[string]string{
		"remote_host":    remoteHost,
		"remote_printer": remotePrinter,
		"mode":           mode,
	})
	resp, err := a.ipcCli.Post("http://ipc/ipc/connect-printer", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("连接失败 (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (a *App) OpenLoginURL(authURL string) {
	if authURL != "" {
		runtime.BrowserOpenURL(a.ctx, authURL)
	}
}
