package zinio

import (
	"fmt"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// PDFium (Chrome's PDF engine, embedded as WebAssembly) handles the page
// PDFs Zinio serves: decryption, page extraction and merging. pdfcpu was
// abandoned because it corrupted these files in several independent ways
// (dropped font objects and OCGs, missed inline /Encrypt dicts, broken
// object-stream output).
var (
	pdfPool     pdfium.Pool
	pdfInstance pdfium.Pdfium
	pdfMu       sync.Mutex
)

// InitPDF starts the embedded PDFium worker. Call once at startup.
func InitPDF() error {
	var err error
	pdfPool, err = webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 1, MaxTotal: 1})
	if err != nil {
		return fmt.Errorf("failed to init PDFium: %w", err)
	}
	pdfInstance, err = pdfPool.GetInstance(30 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to get PDFium instance: %w", err)
	}
	return nil
}

// ClosePDF releases the PDFium worker.
func ClosePDF() {
	if pdfInstance != nil {
		pdfInstance.Close()
	}
	if pdfPool != nil {
		pdfPool.Close()
	}
}

// openWithPasswords opens a page PDF trying each password candidate in order.
func openWithPasswords(path string, passwords []string) (references.FPDF_DOCUMENT, error) {
	pdfMu.Lock()
	defer pdfMu.Unlock()
	var lastErr error
	for _, pw := range passwords {
		p := pw
		resp, err := pdfInstance.OpenDocument(&requests.OpenDocument{FilePath: &path, Password: &p})
		if err == nil {
			return resp.Document, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func closeDoc(doc references.FPDF_DOCUMENT) {
	pdfMu.Lock()
	defer pdfMu.Unlock()
	pdfInstance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc})
}

// mergeIssue builds the final magazine PDF: the first page of every page
// file, in order, into one document.
func mergeIssue(pagePaths []string, outPath string, passwords []string) error {
	pdfMu.Lock()
	dest, err := pdfInstance.FPDF_CreateNewDocument(&requests.FPDF_CreateNewDocument{})
	pdfMu.Unlock()
	if err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	defer closeDoc(dest.Document)

	for i, p := range pagePaths {
		src, err := openWithPasswords(p, passwords)
		if err != nil {
			return fmt.Errorf("open %s: %w", p, err)
		}
		pdfMu.Lock()
		_, err = pdfInstance.FPDF_ImportPagesByIndex(&requests.FPDF_ImportPagesByIndex{
			Source:      src,
			Destination: dest.Document,
			PageIndices: []int{0},
			Index:       i,
		})
		pdfMu.Unlock()
		closeDoc(src)
		if err != nil {
			return fmt.Errorf("import %s: %w", p, err)
		}
	}

	pdfMu.Lock()
	defer pdfMu.Unlock()

	// Zinio pages carry a sticky-note annotation with the page number —
	// noise in the final magazine, so strip all annotations.
	for i := range pagePaths {
		page := requests.Page{ByIndex: &requests.PageByIndex{Document: dest.Document, Index: i}}
		cnt, err := pdfInstance.FPDFPage_GetAnnotCount(&requests.FPDFPage_GetAnnotCount{Page: page})
		if err != nil {
			continue
		}
		for a := cnt.Count - 1; a >= 0; a-- {
			pdfInstance.FPDFPage_RemoveAnnot(&requests.FPDFPage_RemoveAnnot{Page: page, Index: a})
		}
	}

	if _, err := pdfInstance.FPDF_SaveAsCopy(&requests.FPDF_SaveAsCopy{
		Flags:    requests.SaveFlagNoIncremental,
		Document: dest.Document,
		FilePath: &outPath,
	}); err != nil {
		return fmt.Errorf("save %s: %w", outPath, err)
	}
	return nil
}
