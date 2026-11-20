//go:build windows

package client

import (
	"fmt"
	"os/exec"
	"strings"
)

func RegisterIPPPrinter(displayName, ippURL string) error {
	psScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$url = '%s'
$name = '%s'

if (Get-Printer -Name $name -ErrorAction SilentlyContinue) {
    Remove-Printer -Name $name
}

try {
    Add-Printer -IppURL $url
    $p = Get-Printer | Where-Object { $_.PortName -like "*127.0.0.1*" -or $_.Name -eq "PrintMesh Virtual Printer" } | Select-Object -First 1
    if ($p -and $p.Name -ne $name) {
        Rename-Printer -Name $p.Name -NewName $name
    }
} catch {
    Add-Printer -Name $name -PortName $url -DriverName "Microsoft IPP Class Driver"
}
`, ippURL, strings.ReplaceAll(displayName, "'", "''"))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("register printer failed: %w, output: %s", err, string(out))
	}
	return nil
}
