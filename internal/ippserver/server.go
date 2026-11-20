package ippserver

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/OpenPrinting/goipp"
)

type Handler struct {
	PrinterURI string
	OnPDFReady func(pdfPath string) error
	jobSeq     atomic.Int32
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "IPP requires POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req goipp.Message
	if err := req.Decode(r.Body); err != nil {
		log.Printf("IPP decode error: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	op := goipp.Op(req.Code)
	var resp *goipp.Message
	switch op {
	case goipp.OpGetPrinterAttributes:
		resp = h.handleGetPrinterAttributes(req.RequestID)
	case goipp.OpValidateJob:
		resp = goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	case goipp.OpCreateJob:
		jobID := int(h.jobSeq.Add(1))
		resp = h.jobResponse(req.RequestID, jobID)
	case goipp.OpPrintJob, goipp.OpSendDocument:
		jobID := int(h.jobSeq.Add(1))
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("printmesh-job-%d.pdf", jobID))
		if err := saveStream(r.Body, tmpFile); err != nil {
			resp = goipp.NewResponse(goipp.DefaultVersion, goipp.StatusErrorInternal, req.RequestID)
		} else {
			if h.OnPDFReady != nil {
				go func() {
					if err := h.OnPDFReady(tmpFile); err != nil {
						log.Printf("print callback error: %v", err)
					}
				}()
			}
			resp = h.jobResponse(req.RequestID, jobID)
		}
	default:
		resp = goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, req.RequestID)
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", goipp.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) handleGetPrinterAttributes(reqID uint32) *goipp.Message {
	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, reqID)
	resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en")))

	resp.Printer = goipp.Attributes{
		goipp.MakeAttribute("printer-uri-supported", goipp.TagURI, goipp.String(h.PrinterURI)),
		goipp.MakeAttribute("uri-security-supported", goipp.TagKeyword, goipp.String("none")),
		goipp.MakeAttribute("uri-authentication-supported", goipp.TagKeyword, goipp.String("none")),
		goipp.MakeAttribute("printer-name", goipp.TagName, goipp.String("PrintMesh Virtual Printer")),
		goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(3)),
		goipp.MakeAttribute("printer-state-reasons", goipp.TagKeyword, goipp.String("none")),
		goipp.MakeAttribute("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(true)),
		goipp.MakeAttribute("ipp-versions-supported", goipp.TagKeyword, goipp.String("2.0")),
		goipp.MakeAttr("operations-supported", goipp.TagEnum,
			goipp.Integer(goipp.OpPrintJob),
			goipp.Integer(goipp.OpValidateJob),
			goipp.Integer(goipp.OpCreateJob),
			goipp.Integer(goipp.OpSendDocument),
			goipp.Integer(goipp.OpGetPrinterAttributes),
		),
		goipp.MakeAttribute("document-format-default", goipp.TagMimeType, goipp.String("application/pdf")),
		goipp.MakeAttr("document-format-supported", goipp.TagMimeType,
			goipp.String("application/pdf"),
			goipp.String("application/octet-stream"),
		),
		goipp.MakeAttribute("charset-configured", goipp.TagCharset, goipp.String("utf-8")),
		goipp.MakeAttribute("charset-supported", goipp.TagCharset, goipp.String("utf-8")),
		goipp.MakeAttribute("natural-language-configured", goipp.TagLanguage, goipp.String("en")),
		goipp.MakeAttribute("generated-natural-language-supported", goipp.TagLanguage, goipp.String("en")),
		goipp.MakeAttribute("media-default", goipp.TagKeyword, goipp.String("iso_a4_210x297mm")),
		goipp.MakeAttr("media-supported", goipp.TagKeyword,
			goipp.String("iso_a4_210x297mm"),
			goipp.String("na_letter_8.5x11in"),
		),
		goipp.MakeAttribute("color-supported", goipp.TagBoolean, goipp.Boolean(true)),
		goipp.MakeAttribute("sides-default", goipp.TagKeyword, goipp.String("one-sided")),
		goipp.MakeAttr("sides-supported", goipp.TagKeyword,
			goipp.String("one-sided"),
			goipp.String("two-sided-long-edge"),
		),
	}
	return resp
}

func (h *Handler) jobResponse(reqID uint32, jobID int) *goipp.Message {
	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, reqID)
	resp.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	resp.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en")))
	resp.Job.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(jobID)))
	resp.Job.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(fmt.Sprintf("%s/jobs/%d", h.PrinterURI, jobID))))
	resp.Job.Add(goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(9)))
	return resp
}

func saveStream(r io.Reader, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
