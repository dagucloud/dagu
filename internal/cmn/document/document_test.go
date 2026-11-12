// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package document_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/japanese"
)

// pdf builds a one-page-per-entry PDF; an entry is the text drawn on its
// page, and an empty entry leaves the page blank, as a scan is.
func pdf(t *testing.T, pages ...string) []byte {
	t.Helper()
	var objects []string
	kids := make([]string, 0, len(pages))
	for i, text := range pages {
		pageObj := 4 + 2*i
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
		content := ""
		if text != "" {
			content = fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", text)
		}
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", pageObj+1),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		)
	}
	objects = append([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}, objects...)
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestReadPDFTextAndScannedPages(t *testing.T) {
	t.Parallel()

	file, err := document.Read(context.Background(), write(t, "invoice.pdf", pdf(t, "Invoice number 12345 total 9800 yen", "")))
	require.NoError(t, err)
	assert.Equal(t, "invoice.pdf", file.Name)
	require.Len(t, file.Pages, 2)
	assert.Contains(t, file.Pages[0].Text, "Invoice number 12345")
	assert.Nil(t, file.Pages[0].Image)
	require.NotNil(t, file.Pages[1].Image, "a page without text is given as an image")
	assert.Equal(t, "image/png", file.Pages[1].Image.MediaType)
	config, err := png.DecodeConfig(bytes.NewReader(file.Pages[1].Image.Data))
	require.NoError(t, err)
	assert.Equal(t, 1568, max(config.Width, config.Height), "the page is rendered at the long edge a model takes")
	assert.Len(t, file.Images(), 1)
}

func TestReadImageScalesDown(t *testing.T) {
	t.Parallel()

	large := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	for x := range 4000 {
		large.Set(x, 0, color.RGBA{R: 255, A: 255})
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, large))

	file, err := document.Read(context.Background(), write(t, "photo.png", buf.Bytes()))
	require.NoError(t, err)
	require.NotNil(t, file.Image)
	config, err := png.DecodeConfig(bytes.NewReader(file.Image.Data))
	require.NoError(t, err)
	assert.Equal(t, [2]int{1568, 1176}, [2]int{config.Width, config.Height})
	assert.Empty(t, file.Text)
}

func TestReadImageThatFitsIsKept(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 10))))

	file, err := document.Read(context.Background(), write(t, "icon.png", buf.Bytes()))
	require.NoError(t, err)
	require.NotNil(t, file.Image)
	assert.Equal(t, buf.Bytes(), file.Image.Data)
	assert.Equal(t, "image/png", file.Image.MediaType)
}

func TestReadWord(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	book := zip.NewWriter(&buf)
	part, err := book.Create("word/document.xml")
	require.NoError(t, err)
	_, err = part.Write([]byte(`<w:document xmlns:w="w"><w:body>` +
		`<w:p><w:r><w:t>Rules of</w:t></w:r><w:r><w:t xml:space="preserve"> payment</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Item</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Amount</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`</w:body></w:document>`))
	require.NoError(t, err)
	require.NoError(t, book.Close())

	file, err := document.Read(context.Background(), write(t, "rules.docx", buf.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, "Rules of payment\nItem\tAmount\n", file.Text)
}

func TestReadWorkbook(t *testing.T) {
	t.Parallel()

	book := excelize.NewFile()
	require.NoError(t, book.SetSheetName("Sheet1", "Orders"))
	require.NoError(t, book.SetSheetRow("Orders", "A1", &[]any{"order", "amount"}))
	require.NoError(t, book.SetSheetRow("Orders", "A2", &[]any{"A-1", 9800}))
	var buf bytes.Buffer
	require.NoError(t, book.Write(&buf))

	file, err := document.Read(context.Background(), write(t, "orders.xlsx", buf.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, "# Orders\norder\tamount\nA-1\t9800\n\n", file.Text)
}

func TestReadTextInShiftJIS(t *testing.T) {
	t.Parallel()

	encoded, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("請求書\n"))
	require.NoError(t, err)

	file, err := document.Read(context.Background(), write(t, "memo.txt", encoded))
	require.NoError(t, err)
	assert.Equal(t, "請求書\n", file.Text)
}

func TestReadRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.bin"), []byte{0, 1, 2}, 0o600))

	for _, tc := range []struct {
		name, path, want string
	}{
		{"missing", filepath.Join(dir, "missing.pdf"), "does not exist"},
		{"directory", dir, "is a directory"},
		{"unknown kind", filepath.Join(dir, "data.bin"), "a model reads PDF, Word (.docx), Excel (.xlsx), text, and image files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := document.Read(context.Background(), tc.path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestReadImageRefusesARasterTooLargeToDecode(t *testing.T) {
	t.Parallel()

	// A PNG whose header names a raster of 30000 by 30000 pixels, with no
	// pixel data behind it: the header alone must stop the decoding.
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, body []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(body)))
		b.Write(length[:])
		data := append([]byte(kind), body...)
		b.Write(data)
		var sum [4]byte
		binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(data))
		b.Write(sum[:])
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], 30000)
	binary.BigEndian.PutUint32(header[4:], 30000)
	header[8], header[9] = 8, 6
	chunk("IHDR", header)
	chunk("IEND", nil)

	_, err := document.Read(context.Background(), write(t, "huge.png", b.Bytes()))
	require.ErrorContains(t, err, "30000 by 30000 pixels; a model takes images of up to 64 million pixels")
}
