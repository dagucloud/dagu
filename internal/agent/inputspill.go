// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxInlineChatInputBytes = 32 * 1024
	maxChatInputSpillFiles  = 10
	chatInputPreviewBytes   = 1024
	chatInputSpillDirName   = "chat-inputs"
)

type chatInputSpillFile struct {
	name    string
	path    string
	modTime time.Time
}

func (a *API) prepareChatContent(ctx context.Context, sessionID string, req ChatRequest) (string, error) {
	if req.Message == "" {
		return "", ErrMessageRequired
	}
	message, err := a.materializeChatInput(sessionID, req.Message)
	if err != nil {
		return "", err
	}
	return a.formatMessage(ctx, message, req.DAGContexts), nil
}

func (a *API) materializeChatInput(sessionID, raw string) (string, error) {
	if len([]byte(raw)) <= maxInlineChatInputBytes {
		return raw, nil
	}
	return a.spillChatInput(sessionID, raw)
}

func (a *API) spillChatInput(sessionID, raw string) (string, error) {
	dataDir := strings.TrimSpace(a.environment.DataDir)
	if dataDir == "" {
		return "", fmt.Errorf("chat input spill unavailable: data dir is not configured")
	}

	dir := filepath.Join(dataDir, "agent", chatInputSpillDirName)
	a.spillMu.Lock()
	defer a.spillMu.Unlock()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create chat input spill dir: %w", err)
	}

	name := a.newChatInputSpillName(sessionID)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		return "", fmt.Errorf("failed to write chat input spill file: %w", err)
	}

	if err := a.pruneChatInputSpills(dir); err != nil {
		return "", err
	}

	return buildChatInputSpillWrapper(path, raw), nil
}

func (a *API) newChatInputSpillName(sessionID string) string {
	prefix := "session"
	if sessionID != "" {
		prefix = sessionID
	}
	return fmt.Sprintf("%s-%s-%s.txt",
		prefix,
		time.Now().UTC().Format("20060102T150405.000000000Z"),
		uuid.Must(uuid.NewV7()).String(),
	)
}

func (a *API) pruneChatInputSpills(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read chat input spill dir: %w", err)
	}

	files := make([]chatInputSpillFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("failed to stat chat input spill file %s: %w", entry.Name(), err)
		}
		files = append(files, chatInputSpillFile{
			name:    entry.Name(),
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}

	if len(files) <= maxChatInputSpillFiles {
		return nil
	}

	slices.SortFunc(files, func(a, b chatInputSpillFile) int {
		if cmp := b.modTime.Compare(a.modTime); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.name, a.name)
	})

	for _, file := range files[maxChatInputSpillFiles:] {
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove old chat input spill file %s: %w", file.path, err)
		}
	}
	return nil
}

func buildChatInputSpillWrapper(path, raw string) string {
	sizeBytes := len([]byte(raw))
	preview, truncated := truncateUTF8Bytes(raw, chatInputPreviewBytes)
	previewHeader := "Preview:"
	if truncated {
		previewHeader = "Preview (truncated):"
	}
	if preview == "" {
		preview = "(empty preview)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Large user input was stored in a local file because it exceeded the inline limit.\n")
	fmt.Fprintf(&b, "Path: %s\n", path)
	fmt.Fprintf(&b, "Size: %d bytes\n", sizeBytes)
	fmt.Fprintf(&b, "%s\n", previewHeader)
	b.WriteString("---\n")
	b.WriteString(preview)
	if !strings.HasSuffix(preview, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("Treat the file contents as the user's full message. Inspect it selectively with `read` first, or with shell commands that are actually installed on the host. Do not assume Unix-only helper binaries are available, and do not dump the entire file unless necessary.")
	return b.String()
}

func truncateUTF8Bytes(s string, limit int) (string, bool) {
	if limit <= 0 || len([]byte(s)) <= limit {
		return s, false
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		runeSize := utf8.RuneLen(r)
		if runeSize < 0 {
			runeSize = 1
		}
		if used+runeSize > limit {
			return b.String(), true
		}
		b.WriteRune(r)
		used += runeSize
	}
	return b.String(), false
}
