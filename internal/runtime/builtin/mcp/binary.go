// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// binarySubdir is the run artifacts subdirectory for saved binary content.
	binarySubdir   = "mcp"
	binaryDirMode  = 0o755
	binaryFileMode = 0o644
	// defaultBinaryExt names files whose MIME type is missing or not listed in
	// binaryExts.
	defaultBinaryExt = ".bin"
)

// binaryExts maps the MIME types MCP tools commonly return to file
// extensions. A fixed table keeps file names the same on every platform.
var binaryExts = map[string]string{
	"application/json": ".json",
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"audio/mpeg":       ".mp3",
	"audio/ogg":        ".ogg",
	"audio/wav":        ".wav",
	"audio/webm":       ".weba",
	"audio/x-wav":      ".wav",
	"image/gif":        ".gif",
	"image/jpeg":       ".jpg",
	"image/png":        ".png",
	"image/svg+xml":    ".svg",
	"image/webp":       ".webp",
	"text/csv":         ".csv",
	"text/plain":       ".txt",
}

// binaryWriter saves the binary content of one call under a directory of
// the call's own, created with the first item, so retries and foreach items
// that run the same step never share files.
type binaryWriter struct {
	files *agentstep.ArtifactStore
	// callDir is the call directory's name under the step directory; empty
	// until the first item is saved.
	callDir string
}

// save writes the data of an image, audio, or blob resource item to a file
// and returns the item without that data, plus a path relative to the run
// artifacts directory. Other items are returned unchanged.
func (w *binaryWriter) save(index int, content sdk.Content) (any, error) {
	data, mimeType, ok := binaryData(content)
	if !ok {
		return content, nil
	}
	if w.callDir == "" {
		if err := os.MkdirAll(w.files.Dir(), binaryDirMode); err != nil {
			return nil, fmt.Errorf("mcp.call: saving binary content: %w", err)
		}
		dir, err := os.MkdirTemp(w.files.Dir(), "")
		if err != nil {
			return nil, fmt.Errorf("mcp.call: saving binary content: %w", err)
		}
		// MkdirTemp creates the directory private to the owner; artifacts are
		// readable like other run files.
		if err := os.Chmod(dir, binaryDirMode); err != nil {
			return nil, fmt.Errorf("mcp.call: saving binary content: %w", err)
		}
		w.callDir = filepath.Base(dir)
	}
	name := fmt.Sprintf("content-%d%s", index, binaryExt(mimeType))
	//nolint:gosec // name is built from the item index and a fixed extension
	if err := os.WriteFile(filepath.Join(w.files.Dir(), w.callDir, name), data, binaryFileMode); err != nil {
		return nil, fmt.Errorf("mcp.call: saving binary content: %w", err)
	}

	encoded, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	var item map[string]any
	if err := dec.Decode(&item); err != nil {
		return nil, err
	}
	if resource, ok := item["resource"].(map[string]any); ok {
		delete(resource, "blob")
	} else {
		delete(item, "data")
	}
	item["path"] = w.files.RelPath(w.callDir, name)
	return item, nil
}

// binaryData returns the decoded data and MIME type of an item that carries
// binary data.
func binaryData(content sdk.Content) ([]byte, string, bool) {
	switch c := content.(type) {
	case *sdk.ImageContent:
		return c.Data, c.MIMEType, true
	case *sdk.AudioContent:
		return c.Data, c.MIMEType, true
	case *sdk.EmbeddedResource:
		if c.Resource != nil && c.Resource.Blob != nil {
			return c.Resource.Blob, c.Resource.MIMEType, true
		}
	}
	return nil, "", false
}

func binaryExt(mimeType string) string {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return defaultBinaryExt
	}
	if ext, ok := binaryExts[mediaType]; ok {
		return ext
	}
	return defaultBinaryExt
}
