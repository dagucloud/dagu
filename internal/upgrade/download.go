// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/go-resty/resty/v2"
)

// DownloadOptions configures the download operation.
type DownloadOptions struct {
	URL          string
	Destination  string // Temp file path
	ExpectedHash string // SHA256 from checksums.txt
	OnProgress   func(downloaded, total int64)
}

// progressWriter wraps an io.Writer to track download progress.
type progressWriter struct {
	writer     io.Writer
	total      int64
	written    int64
	onProgress func(downloaded, total int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.written += int64(n)
	if pw.onProgress != nil {
		pw.onProgress(pw.written, pw.total)
	}
	return n, err
}

// Download downloads a file with checksum verification.
// The entire GET + stream-to-file + checksum-verify is wrapped in a retry loop
// so that mid-stream failures (io.Copy errors) are also retried.
func Download(ctx context.Context, opts DownloadOptions) error {
	dir := filepath.Dir(opts.Destination)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	client := resty.New().SetTimeout(0) // No timeout for downloads, no retry config

	// HEAD for content-length (best-effort, outside retry)
	var contentLength int64
	if opts.OnProgress != nil {
		sizeResp, sizeErr := client.R().SetContext(ctx).Head(opts.URL)
		if sizeErr == nil && sizeResp.StatusCode() == 200 {
			contentLength = sizeResp.RawResponse.ContentLength
		}
	}

	policy := newUpgradeRetryPolicy()

	return backoff.Retry(ctx, func(ctx context.Context) error {
		// Fresh temp file per attempt
		tempFile, err := os.CreateTemp(dir, "dagu-download-*.tmp")
		if err != nil {
			return &nonRetriableError{err: fmt.Errorf("failed to create temp file: %w", err)}
		}
		tempPath := tempFile.Name()
		defer func() {
			_ = tempFile.Close()
			if _, statErr := os.Stat(tempPath); statErr == nil {
				_ = os.Remove(tempPath)
			}
		}()

		// Progress writer (reset per attempt)
		var writer io.Writer = tempFile
		if opts.OnProgress != nil {
			writer = &progressWriter{writer: tempFile, total: contentLength, onProgress: opts.OnProgress}
		}

		// GET
		resp, err := client.R().SetContext(ctx).SetDoNotParseResponse(true).Get(opts.URL)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		code := resp.StatusCode()
		if code != 200 {
			if resp.RawBody() != nil {
				_ = resp.RawBody().Close()
			}
			return &httpError{statusCode: code, message: fmt.Sprintf("download failed with status %d", code)}
		}

		// Stream to file
		if resp.RawBody() != nil {
			_, copyErr := io.Copy(writer, resp.RawBody())
			_ = resp.RawBody().Close()
			if copyErr != nil {
				return fmt.Errorf("failed to write downloaded data: %w", copyErr)
			}
		}

		if err := tempFile.Close(); err != nil {
			return &nonRetriableError{err: fmt.Errorf("failed to close temp file: %w", err)}
		}

		// Verify checksum
		if opts.ExpectedHash != "" {
			if err := VerifyChecksum(tempPath, opts.ExpectedHash); err != nil {
				return &nonRetriableError{err: err}
			}
		}

		// Atomic move
		if err := os.Rename(tempPath, opts.Destination); err != nil {
			return &nonRetriableError{err: fmt.Errorf("failed to move downloaded file: %w", err)}
		}

		return nil
	}, policy, isRetriableError)
}

// VerifyChecksum computes SHA256 and compares with expected hash.
func VerifyChecksum(filePath, expectedHash string) error {
	f, err := os.Open(filePath) //nolint:gosec // filePath is from controlled internal source
	if err != nil {
		return fmt.Errorf("failed to open file for checksum verification: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// FormatBytes formats byte count in human-readable format.
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
