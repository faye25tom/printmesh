//go:build windows

package client

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type RemoteDriverMeta struct {
	PrinterName string `json:"printer_name"`
	DriverName  string `json:"driver_name"`
	Arch        string `json:"arch"`
	SHA256      string `json:"sha256"`
}

func InstallRemoteRawPrinter(tsHTTP *http.Client, remoteHost, remotePrinter, displayName string, localPort int) error {
	metaURL := fmt.Sprintf("http://%s:19631/api/driver/meta?printer=%s", remoteHost, url.QueryEscape(remotePrinter))
	resp, err := tsHTTP.Get(metaURL)
	if err != nil {
		return fmt.Errorf("fetch driver meta failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server driver meta error (%d): %s", resp.StatusCode, string(body))
	}

	var meta RemoteDriverMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return err
	}

	if meta.Arch != runtime.GOARCH {
		return fmt.Errorf("架构不匹配：服务端驱动为 %s，本机为 %s，请改用免驱 IPP 模式", meta.Arch, runtime.GOARCH)
	}

	tmpDir, err := os.MkdirTemp("", "printmesh-drv-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, "driver.zip")
	dlURL := fmt.Sprintf("http://%s:19631/api/driver/download?printer=%s", remoteHost, url.QueryEscape(remotePrinter))
	if err := downloadAndVerify(tsHTTP, dlURL, zipPath, meta.SHA256); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "pkg")
	if err := unzipSafe(zipPath, extractDir); err != nil {
		return err
	}

	// 兼容 Win7 ~ Win11：逐个 .inf 调用 pnputil -a 静默注入 Driver Store
	if err := stageInfDriversCompat(extractDir); err != nil {
		return err
	}

	// 注册驱动并绑定本地 RAW 端口（优先 PowerShell，Win7 自动回退 prnport.vbs + printui.dll）
	return bindRawPrinterCompat(displayName, meta.DriverName, extractDir, localPort)
}

func stageInfDriversCompat(extractDir string) error {
	var infFiles []string
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".inf") {
			infFiles = append(infFiles, path)
		}
		return nil
	})
	for _, inf := range infFiles {
		cmd := exec.Command("pnputil.exe", "-a", inf)
		out, err := cmd.CombinedOutput()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && (exitErr.ExitCode() == 3010 || exitErr.ExitCode() == 259) {
				continue
			}
			return fmt.Errorf("pnputil -a %s failed: %w, out: %s", inf, err, string(out))
		}
	}
	return nil
}

func bindRawPrinterCompat(displayName, driverName, extractDir string, localPort int) error {
	portName := fmt.Sprintf("PrintMesh_RAW_%d", localPort)
	ps := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
if (-not (Get-PrinterDriver -Name '%s' -ErrorAction SilentlyContinue)) {
    Add-PrinterDriver -Name '%s'
}
if (-not (Get-PrinterPort -Name '%s' -ErrorAction SilentlyContinue)) {
    Add-PrinterPort -Name '%s' -PrinterHostAddress '127.0.0.1' -PortNumber %d
}
if (Get-Printer -Name '%s' -ErrorAction SilentlyContinue) {
    Remove-Printer -Name '%s'
}
Add-Printer -Name '%s' -DriverName '%s' -PortName '%s'
`, strings.ReplaceAll(driverName, "'", "''"),
		strings.ReplaceAll(driverName, "'", "''"),
		portName, portName, localPort,
		strings.ReplaceAll(displayName, "'", "''"),
		strings.ReplaceAll(displayName, "'", "''"),
		strings.ReplaceAll(displayName, "'", "''"),
		strings.ReplaceAll(driverName, "'", "''"),
		portName)

	if err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run(); err == nil {
		return nil
	}

	// Windows 7 兜底：使用系统内置 prnport.vbs 与 printui.dll
	prnPortVbs := filepath.Join(os.Getenv("WINDIR"), "System32", "Printing_Admin_Scripts", "zh-CN", "prnport.vbs")
	if _, err := os.Stat(prnPortVbs); os.IsNotExist(err) {
		prnPortVbs = filepath.Join(os.Getenv("WINDIR"), "System32", "Printing_Admin_Scripts", "en-US", "prnport.vbs")
	}
	_ = exec.Command("cscript.exe", "//Nologo", prnPortVbs, "-a", "-r", portName, "-h", "127.0.0.1", "-o", "raw", "-n", fmt.Sprintf("%d", localPort)).Run()

	var firstInf string
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if firstInf == "" && err == nil && !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".inf") {
			firstInf = path
		}
		return nil
	})
	if firstInf == "" {
		return fmt.Errorf("未找到驱动 .inf 文件")
	}
	out, err := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", "/if", "/b", displayName, "/f", firstInf, "/r", portName, "/m", driverName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Win7 PrintUIEntry 绑定打印机失败: %w, out: %s", err, string(out))
	}
	return nil
}

func downloadAndVerify(cli *http.Client, dlURL, dst, expectedSHA string) error {
	resp, err := cli.Get(dlURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download driver failed: status %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return err
	}
	if actual := hex.EncodeToString(h.Sum(nil)); actual != expectedSHA {
		return fmt.Errorf("driver zip sha256 mismatch: expected %s, got %s", expectedSHA, actual)
	}
	return nil
}

func unzipSafe(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()

	cleanDst := filepath.Clean(dstDir) + string(os.PathSeparator)
	for _, f := range r.File {
		target := filepath.Join(dstDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), cleanDst) {
			return fmt.Errorf("illegal zip path: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
