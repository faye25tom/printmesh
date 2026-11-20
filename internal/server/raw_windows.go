//go:build windows

package server

import (
	"fmt"
	"io"
	"syscall"
	"unsafe"
)

type docInfo1W struct {
	pDocName    *uint16
	pOutputFile *uint16
	pDatatype   *uint16
}

var (
	winspool             = syscall.NewLazyDLL("winspool.drv")
	procOpenPrinterW     = winspool.NewProc("OpenPrinterW")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procStartDocPrinterW = winspool.NewProc("StartDocPrinterW")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
)

func PrintRawStreamToWindowsPrinter(printerName, docName string, r io.Reader) error {
	pName, err := syscall.UTF16PtrFromString(printerName)
	if err != nil {
		return err
	}

	var hPrinter uintptr
	ret, _, err := procOpenPrinterW.Call(uintptr(unsafe.Pointer(pName)), uintptr(unsafe.Pointer(&hPrinter)), 0)
	if ret == 0 {
		return fmt.Errorf("OpenPrinterW failed for %q: %w", printerName, err)
	}
	defer procClosePrinter.Call(hPrinter)

	dName, _ := syscall.UTF16PtrFromString(docName)
	dType, _ := syscall.UTF16PtrFromString("RAW")
	di := docInfo1W{
		pDocName:  dName,
		pDatatype: dType,
	}

	jobID, _, err := procStartDocPrinterW.Call(hPrinter, 1, uintptr(unsafe.Pointer(&di)))
	if jobID == 0 {
		return fmt.Errorf("StartDocPrinterW failed: %w", err)
	}
	defer procEndDocPrinter.Call(hPrinter)

	if ret, _, err := procStartPagePrinter.Call(hPrinter); ret == 0 {
		return fmt.Errorf("StartPagePrinter failed: %w", err)
	}
	defer procEndPagePrinter.Call(hPrinter)

	buf := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			writtenTotal := 0
			for writtenTotal < n {
				var written uint32
				chunk := buf[writtenTotal:n]
				ok, _, writeErr := procWritePrinter.Call(
					hPrinter,
					uintptr(unsafe.Pointer(&chunk[0])),
					uintptr(len(chunk)),
					uintptr(unsafe.Pointer(&written)),
				)
				if ok == 0 {
					return fmt.Errorf("WritePrinter failed: %w", writeErr)
				}
				writtenTotal += int(written)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}
