//go:build windows

package server

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	PRINTER_ENUM_LOCAL       = 0x00000002
	PRINTER_ENUM_CONNECTIONS = 0x00000004
)

type printerInfo4W struct {
	pPrinterName *uint16
	pServerName  *uint16
	Attributes   uint32
}

var procEnumPrintersW = winspool.NewProc("EnumPrintersW")

// ListLocalPrinters 使用纯 Win32 API 枚举本机打印机（兼容 Windows 7 到 Windows 11，不依赖 PowerShell）
func ListLocalPrinters() ([]string, error) {
	var needed, returned uint32
	flags := uint32(PRINTER_ENUM_LOCAL | PRINTER_ENUM_CONNECTIONS)

	procEnumPrintersW.Call(
		uintptr(flags),
		0,
		4, // Level 4 = PRINTER_INFO_4W
		0,
		0,
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if needed == 0 {
		return []string{}, nil
	}

	buf := make([]byte, needed)
	ret, _, err := procEnumPrintersW.Call(
		uintptr(flags),
		0,
		4,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(needed),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if ret == 0 {
		return nil, err
	}

	entrySize := unsafe.Sizeof(printerInfo4W{})
	printers := make([]string, 0, returned)
	for i := uint32(0); i < returned; i++ {
		info := (*printerInfo4W)(unsafe.Pointer(&buf[uintptr(i)*entrySize]))
		if info.pPrinterName == nil {
			continue
		}
		name := utf16PtrToString(info.pPrinterName)
		if name != "" && !strings.Contains(name, "(pm-srv-") {
			printers = append(printers, name)
		}
	}
	return printers, nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var chars []uint16
	for ptr := p; *ptr != 0; ptr = (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 2)) {
		chars = append(chars, *ptr)
	}
	return syscall.UTF16ToString(chars)
}
