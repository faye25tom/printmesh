//go:build windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

const (
	HORZRES       = 8
	VERTRES       = 10
	FPDF_PRINTING = 0x800
)

type docInfoW struct {
	cbSize       int32
	lpszDocName  *uint16
	lpszOutput   *uint16
	lpszDatatype *uint16
	fwType       uint32
}

var (
	pdfPrintMu    sync.Mutex
	gdi32         = syscall.NewLazyDLL("gdi32.dll")
	procCreateDCW = gdi32.NewProc("CreateDCW")
	procDeleteDC  = gdi32.NewProc("DeleteDC")
	procStartDocW = gdi32.NewProc("StartDocW")
	procEndDoc    = gdi32.NewProc("EndDoc")
	procStartPage = gdi32.NewProc("StartPage")
	procEndPage   = gdi32.NewProc("EndPage")
	procGetDevCap = gdi32.NewProc("GetDeviceCaps")

	pdfium             = loadPDFiumDLL()
	procInitLibrary    = pdfium.NewProc("FPDF_InitLibrary")
	procDestroyLibrary = pdfium.NewProc("FPDF_DestroyLibrary")
	procLoadMemDoc     = pdfium.NewProc("FPDF_LoadMemDocument")
	procCloseDocument  = pdfium.NewProc("FPDF_CloseDocument")
	procGetPageCount   = pdfium.NewProc("FPDF_GetPageCount")
	procLoadPage       = pdfium.NewProc("FPDF_LoadPage")
	procClosePage      = pdfium.NewProc("FPDF_ClosePage")
	procRenderPage     = pdfium.NewProc("FPDF_RenderPage")
)

func loadPDFiumDLL() *syscall.LazyDLL {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "pdfium.dll")
		if _, err := os.Stat(p); err == nil {
			return syscall.NewLazyDLL(p)
		}
	}
	return syscall.NewLazyDLL("pdfium.dll")
}

func PrintPDFToWindowsPrinter(pdfPath, printerName string) error {
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		return err
	}
	if len(pdfBytes) == 0 {
		return fmt.Errorf("empty pdf file")
	}

	pdfPrintMu.Lock()
	defer pdfPrintMu.Unlock()

	procInitLibrary.Call()
	defer procDestroyLibrary.Call()

	doc, _, _ := procLoadMemDoc.Call(
		uintptr(unsafe.Pointer(&pdfBytes[0])),
		uintptr(len(pdfBytes)),
		0,
	)
	if doc == 0 {
		return fmt.Errorf("FPDF_LoadMemDocument failed for %s", pdfPath)
	}
	defer procCloseDocument.Call(doc)

	pageCountPtr, _, _ := procGetPageCount.Call(doc)
	pageCount := int(pageCountPtr)
	if pageCount <= 0 {
		return fmt.Errorf("pdf has no pages")
	}

	driver, _ := syscall.UTF16PtrFromString("WINSPOOL")
	device, _ := syscall.UTF16PtrFromString(printerName)
	hdc, _, err := procCreateDCW.Call(
		uintptr(unsafe.Pointer(driver)),
		uintptr(unsafe.Pointer(device)),
		0,
		0,
	)
	if hdc == 0 {
		return fmt.Errorf("CreateDCW failed for printer %q: %v", printerName, err)
	}
	defer procDeleteDC.Call(hdc)

	printableW, _, _ := procGetDevCap.Call(hdc, HORZRES)
	printableH, _, _ := procGetDevCap.Call(hdc, VERTRES)

	docName, _ := syscall.UTF16PtrFromString("PrintMesh Remote Job")
	di := docInfoW{
		cbSize:      int32(unsafe.Sizeof(docInfoW{})),
		lpszDocName: docName,
	}
	if ret, _, err := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(&di))); int32(ret) <= 0 {
		return fmt.Errorf("StartDocW failed: %v", err)
	}
	defer procEndDoc.Call(hdc)

	for i := 0; i < pageCount; i++ {
		page, _, _ := procLoadPage.Call(doc, uintptr(i))
		if page == 0 {
			continue
		}
		procStartPage.Call(hdc)
		procRenderPage.Call(hdc, page, 0, 0, printableW, printableH, 0, FPDF_PRINTING)
		procEndPage.Call(hdc)
		procClosePage.Call(page)
	}

	return nil
}
