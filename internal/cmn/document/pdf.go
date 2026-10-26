// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package document

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// pdfiumMu reads one PDF at a time: PDFium's runtime takes a few hundred MB
// while it reads, and is let go after each file.
var pdfiumMu sync.Mutex

// readPDF reads a PDF's pages with PDFium, carried as WebAssembly so every
// system reads the same way. A page with no text of its own is a scan, and
// is rendered to an image at the size a model takes.
func readPDF(ctx context.Context, data []byte) ([]Page, error) {
	pdfiumMu.Lock()
	defer pdfiumMu.Unlock()
	pool, err := webassembly.Init(webassembly.Config{Context: ctx, MaxIdle: 1, MaxTotal: 1})
	if err != nil {
		return nil, fmt.Errorf("the PDF reader could not start: %w", err)
	}
	defer pool.Close()
	reader, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("the PDF reader could not start: %w", err)
	}
	defer reader.Close()
	doc, err := reader.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return nil, fmt.Errorf("the PDF could not be read: %w", err)
	}
	defer reader.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	count, err := reader.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("the PDF could not be read: %w", err)
	}
	pages := make([]Page, 0, count.PageCount)
	for i := range count.PageCount {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("the PDF could not be read: %w", err)
		}
		page := requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}}
		text, err := reader.GetPageText(&requests.GetPageText{Page: page})
		if err != nil {
			return nil, fmt.Errorf("the PDF could not be read: %w", err)
		}
		if utf8.RuneCountInString(strings.TrimSpace(text.Text)) >= scannedPageRunes {
			pages = append(pages, Page{Text: text.Text})
			continue
		}
		image, err := renderPage(reader, doc.Document, i)
		if err != nil {
			return nil, fmt.Errorf("page %d of the PDF could not be rendered: %w", i+1, err)
		}
		pages = append(pages, Page{Image: image})
	}
	return pages, nil
}

// renderPage renders a page at the size a model takes.
func renderPage(reader pdfium.Pdfium, doc references.FPDF_DOCUMENT, index int) (*Image, error) {
	size, err := reader.FPDF_GetPageSizeByIndexF(&requests.FPDF_GetPageSizeByIndexF{Document: doc, Index: index})
	if err != nil {
		return nil, err
	}
	// Points are 1/72 inch; the DPI puts the page's long edge at the model's.
	dpi := int(float64(imageLongEdge) * 72 / float64(max(size.Size.Width, size.Size.Height, 1)))
	dpi = min(max(dpi, 36), 300)
	rendered, err := reader.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: index}},
		DPI:  dpi,
	})
	if err != nil {
		return nil, err
	}
	defer rendered.Cleanup()
	// The pixels live in the WebAssembly memory until Cleanup, so they are
	// copied out before it.
	return encodeFitted(toRGBA(rendered.Result.RenderedImage))
}
