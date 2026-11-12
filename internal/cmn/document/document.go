// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package document reads the files a model is given: PDF, Word and Excel
// files and text as text, images as images, and the scanned pages of a PDF
// as images.
package document

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // decoded by image.Decode
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/imagefit"
	"github.com/xuri/excelize/v2"
	_ "golang.org/x/image/webp" // decoded by image.Decode
	"golang.org/x/text/encoding/japanese"
)

const (
	// MaxFileBytes bounds one file.
	MaxFileBytes = 50 << 20
	// MaxImages bounds the images a model is given at once, image files and
	// scanned pages together.
	MaxImages = 50
	// imageLongEdge is the longest edge of an image a model is given; a
	// larger image is scaled down to it.
	imageLongEdge = 1568
	// maxImagePixels bounds the pixels decoded from one image, since a
	// small file can name a raster that does not fit in memory.
	maxImagePixels = 64_000_000
	// maxImageBytes bounds one encoded image, as the providers do.
	maxImageBytes = 4 << 20
	// scannedPageRunes is the text below which a PDF page counts as scanned
	// and is given as an image.
	scannedPageRunes = 20
)

// Image is an encoded image.
type Image struct {
	// MediaType is the image's IANA media type, such as image/png.
	MediaType string
	Data      []byte
}

// Page is one page of a PDF: its text, or its image when it holds none.
type Page struct {
	Text  string
	Image *Image
}

// File is a file as a model takes it.
type File struct {
	// Name is the file's base name.
	Name string
	// Text is the file's text; a PDF's text is in Pages instead.
	Text string
	// Image is an image file, scaled to fit a model.
	Image *Image
	// Pages are a PDF's pages.
	Pages []Page
}

// Images returns the file's images: the image file, or the scanned pages.
func (f File) Images() []Image {
	if f.Image != nil {
		return []Image{*f.Image}
	}
	var images []Image
	for _, page := range f.Pages {
		if page.Image != nil {
			images = append(images, *page.Image)
		}
	}
	return images
}

// Read reads one file. PDF text is read with PDFium, the PDF engine of
// Chrome, so the embedded fonts Japanese documents use are read; a page
// with no text is rendered to an image. Word and Excel files are read as
// text, a paragraph or row a line; text files in UTF-8 or Shift_JIS; images
// are scaled down when larger than a model takes.
func Read(ctx context.Context, path string) (File, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, fmt.Errorf("file %s does not exist", path)
	}
	if err != nil {
		return File{}, err
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("%s is a directory; name the files in it, such as %s", path, filepath.Join(path, "*.pdf"))
	}
	if !info.Mode().IsRegular() {
		return File{}, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > MaxFileBytes {
		return File{}, fmt.Errorf("%s is %d MB; a model takes files of up to %d MB", path, (info.Size()+(1<<20)-1)>>20, MaxFileBytes>>20)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is a file the step named
	if err != nil {
		return File{}, err
	}
	file := File{Name: filepath.Base(path)}
	switch kind := kindOf(path, data); kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		file.Image, err = fitImage(data, kind)
	case "application/pdf":
		file.Pages, err = readPDF(ctx, data)
	case "docx":
		file.Text, err = docxText(data)
	case "xlsx":
		file.Text, err = workbookText(data)
	case "text":
		file.Text, err = plainText(data)
	default:
		return File{}, fmt.Errorf("%s cannot be read: a model reads PDF, Word (.docx), Excel (.xlsx), text, and image files", path)
	}
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// textExtensions are the file name extensions read as plain text.
var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".csv": true, ".tsv": true, ".json": true, ".jsonl": true,
	".yaml": true, ".yml": true, ".xml": true, ".html": true, ".htm": true, ".log": true,
}

// kindOf tells a file's kind by its content, and by its name for the
// kinds that are archives or text.
func kindOf(path string, data []byte) string {
	switch kind := http.DetectContentType(data); kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf":
		return kind
	}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".docx":
		return "docx"
	case ".xlsx", ".xlsm":
		return "xlsx"
	default:
		if textExtensions[ext] {
			return "text"
		}
	}
	return ""
}

// fitImage scales an image down to what a model takes, and keeps one that
// fits as it is.
func fitImage(data []byte, mediaType string) (*Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w", err)
	}
	if max(config.Width, config.Height) <= imageLongEdge && len(data) <= maxImageBytes {
		return &Image{MediaType: mediaType, Data: data}, nil
	}
	if int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, fmt.Errorf("the image is %d by %d pixels; a model takes images of up to %d million pixels", config.Width, config.Height, maxImagePixels/1_000_000)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w", err)
	}
	return encodeFitted(toRGBA(decoded))
}

// toRGBA copies an image into RGBA.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	bounds := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	return rgba
}

// encodeFitted scales an image to the long edge a model takes and encodes
// it as PNG, or as JPEG when the PNG is too large.
func encodeFitted(img *image.RGBA) (*Image, error) {
	bounds := img.Bounds()
	width, height := imagefit.FitSize(bounds.Dx(), bounds.Dy(), imageLongEdge, 0)
	if width != bounds.Dx() || height != bounds.Dy() {
		img = imagefit.Resize(img, width, height)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	if buf.Len() <= maxImageBytes {
		return &Image{MediaType: "image/png", Data: buf.Bytes()}, nil
	}
	buf.Reset()
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	if buf.Len() > maxImageBytes {
		return nil, fmt.Errorf("the image is %d MB once encoded; a model takes images of up to %d MB", (buf.Len()+(1<<20)-1)>>20, maxImageBytes>>20)
	}
	return &Image{MediaType: "image/jpeg", Data: buf.Bytes()}, nil
}

// docxText reads a Word document's paragraphs and tables: a paragraph a
// line, and a table row a line with its cells set apart by tabs.
func docxText(data []byte) (string, error) {
	book, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("the Word file could not be read: %w", err)
	}
	part, err := book.Open("word/document.xml")
	if err != nil {
		return "", fmt.Errorf("the Word file could not be read: %w", err)
	}
	defer func() { _ = part.Close() }()
	var out, line strings.Builder
	var cell, row []string
	inText, inCell := false, false
	decoder := xml.NewDecoder(io.LimitReader(part, 64<<20))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return out.String(), nil
		}
		if err != nil {
			return "", fmt.Errorf("the Word file could not be read: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				line.WriteString("\t")
			case "br":
				line.WriteString(" ")
			case "tc":
				inCell, cell = true, nil
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inCell {
					cell = append(cell, line.String())
				} else {
					out.WriteString(line.String() + "\n")
				}
				line.Reset()
			case "tc":
				inCell, row = false, append(row, strings.Join(cell, " "))
			case "tr":
				out.WriteString(strings.Join(row, "\t") + "\n")
				row = nil
			}
		case xml.CharData:
			if inText {
				line.Write(t)
			}
		}
	}
}

// workbookText reads every sheet of a workbook, a row a line and its cells
// set apart by tabs, under a heading naming the sheet.
func workbookText(data []byte) (string, error) {
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("the Excel file could not be read: %w", err)
	}
	defer func() { _ = book.Close() }()
	var b strings.Builder
	for _, sheet := range book.GetSheetList() {
		rows, err := book.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("the Excel file could not be read: %w", err)
		}
		fmt.Fprintf(&b, "# %s\n", sheet)
		for _, row := range rows {
			if line := strings.TrimRight(strings.Join(row, "\t"), "\t "); line != "" {
				b.WriteString(line + "\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// plainText reads a text file, in UTF-8 or else Shift_JIS, as Japanese
// Windows writes them.
func plainText(data []byte) (string, error) {
	if utf8.Valid(data) {
		return string(data), nil
	}
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(data)
	if err != nil {
		return "", fmt.Errorf("the text file could not be read: %w", err)
	}
	return string(decoded), nil
}
