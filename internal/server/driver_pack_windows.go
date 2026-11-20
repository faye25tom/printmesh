//go:build windows

package server

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type DriverMeta struct {
	PrinterName string `json:"printer_name"`
	DriverName  string `json:"driver_name"`
	Arch        string `json:"arch"`
	SHA256      string `json:"sha256"`
	ZipPath     string `json:"-"`
}

type psDriverInfo struct {
	DriverName string `json:"DriverName"`
	InfPath    string `json:"InfPath"`
}

func PrepareDriverPackage(printerName, cacheDir, manualDir string) (*DriverMeta, error) {
	ps := fmt.Sprintf(`
$p = Get-Printer -Name '%s' -ErrorAction Stop
$d = Get-PrinterDriver -Name $p.DriverName -ErrorAction Stop
[PSCustomObject]@{ DriverName = $d.Name; InfPath = $d.InfPath } | ConvertTo-Json -Compress
`, strings.ReplaceAll(printerName, "'", "''"))

	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("query printer driver failed: %w, out: %s", err, string(out))
	}

	var info psDriverInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse driver json failed: %w", err)
	}

	_ = os.MkdirAll(cacheDir, 0755)
	safeFileName := strings.NewReplacer("\\", "_", "/", "_", ":", "_", " ", "_").Replace(info.DriverName) + ".zip"
	zipPath := filepath.Join(cacheDir, safeFileName)

	manualZip := filepath.Join(manualDir, safeFileName)
	if _, err := os.Stat(manualZip); err == nil {
		zipPath = manualZip
	} else if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		if info.InfPath == "" || !strings.Contains(strings.ToLower(info.InfPath), "filerepository") {
			return nil, fmt.Errorf("驱动 %q 不在 DriverStore 中，请将官方解压驱动打包为 %s 后放入手动目录", info.DriverName, safeFileName)
		}
		driverDir := filepath.Dir(info.InfPath)
		if err := zipDirectory(driverDir, zipPath); err != nil {
			return nil, fmt.Errorf("zip driver dir failed: %w", err)
		}
	}

	hash, err := fileSHA256(zipPath)
	if err != nil {
		return nil, err
	}

	return &DriverMeta{
		PrinterName: printerName,
		DriverName:  info.DriverName,
		Arch:        runtime.GOARCH,
		SHA256:      hash,
		ZipPath:     zipPath,
	}, nil
}

func zipDirectory(srcDir, dstZip string) error {
	f, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(relPath))
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
