//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

var Version = "dev"

func main() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatal(err)
	}
	if isService {
		if err := svc.Run(ServiceName, &printMeshService{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			if err := installService(); err != nil {
				log.Fatalf("安装服务失败: %v", err)
			}
			fmt.Println("PrintMesh 服务安装并启动成功")
			return
		case "uninstall":
			if err := uninstallService(); err != nil {
				log.Fatalf("卸载服务失败: %v", err)
			}
			fmt.Println("PrintMesh 服务已卸载")
			return
		case "version":
			fmt.Println(Version)
			return
		}
	}

	log.Println("以控制台调试模式启动 PrintMesh Daemon...")
	ctx := context.Background()
	eng := NewEngine()
	go eng.Start(ctx)
	if _, err := startIPCServer(ctx, eng); err != nil {
		log.Fatal(err)
	}
	select {}
}

func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, _ = filepath.Abs(exePath)

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	if s, err := m.OpenService(ServiceName); err == nil {
		s.Close()
		return fmt.Errorf("服务 %s 已存在", ServiceName)
	}

	s, err := m.CreateService(ServiceName, exePath, mgr.Config{
		DisplayName: "PrintMesh Remote Printing Service",
		Description: "基于 Tailscale 的异地组网打印后台服务",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Start()
}

func uninstallService() error {
	cleanupPS := `
Get-Printer | Where-Object { $_.PortName -like "PrintMesh_*" -or $_.PortName -like "*127.0.0.1:1963*" } | Remove-Printer -ErrorAction SilentlyContinue
Get-PrinterPort | Where-Object { $_.Name -like "PrintMesh_*" } | Remove-PrinterPort -ErrorAction SilentlyContinue
`
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cleanupPS).Run()

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()

	_, _ = s.Control(svc.Stop)
	return s.Delete()
}
