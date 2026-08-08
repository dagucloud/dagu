// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	tokenSecretFileName   = "token_secret"
	tokenSecretByteLength = 32
	tokenSecretDirPerm    = 0o700
	tokenSecretFilePerm   = 0o600
)

var _ auth.TokenSecretProvider = (*tokenSecretProvider)(nil)

type tokenSecretProvider struct {
	dir string
}

func (s *tokenSecretProvider) Resolve(_ context.Context) (auth.TokenSecret, error) {
	path := filepath.Join(s.dir, tokenSecretFileName)

	fileExists := false
	data, err := fileutil.ReadFile(path)
	if err == nil {
		fileExists = true
		// File exists — check if it has usable content.
		content := strings.TrimSpace(string(data))
		if content != "" {
			return auth.NewTokenSecretFromString(content)
		}
		// Empty/whitespace-only file — treat as missing, fall through to generation.
	} else if !errors.Is(err, os.ErrNotExist) {
		// Permission error or other I/O failure — fatal, do not skip.
		return auth.TokenSecret{}, fmt.Errorf("failed to read token secret file %s: %w", path, err)
	}

	// Generate a new secret.
	secret, err := generateTokenSecret()
	if err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to generate token secret: %w", err)
	}

	// Ensure directory exists with correct permissions.
	if err := os.MkdirAll(s.dir, tokenSecretDirPerm); err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to create auth directory %s: %w", s.dir, err)
	}
	if err := os.Chmod(s.dir, tokenSecretDirPerm); err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to set auth directory permissions %s: %w", s.dir, err)
	}

	if fileExists {
		// Remove the empty file so the target can be created atomically.
		if err := fileutil.Remove(path); err != nil {
			return auth.TokenSecret{}, fmt.Errorf("failed to remove empty token secret file %s: %w", path, err)
		}
	}

	// Use exclusive create to prevent race conditions.
	// If another process created the file first, read the persisted secret.
	if err := writeTokenSecretExclusive(path, []byte(secret), tokenSecretFilePerm); err != nil {
		if errors.Is(err, os.ErrExist) {
			data, readErr := fileutil.ReadFile(path)
			if readErr != nil {
				return auth.TokenSecret{}, fmt.Errorf("failed to read token secret after race: %w", readErr)
			}
			return auth.NewTokenSecretFromString(strings.TrimSpace(string(data)))
		}
		return auth.TokenSecret{}, fmt.Errorf("failed to write token secret file %s: %w", path, err)
	}

	return auth.NewTokenSecretFromString(secret)
}

func generateTokenSecret() (string, error) {
	buf := make([]byte, tokenSecretByteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// writeTokenSecretExclusive atomically creates a file, failing if it already exists.
// Writes to a temp file first, then hard-links to the target path. This ensures
// that if the target file exists, it always contains complete content (no partial reads).
// Returns os.ErrExist if the file already exists (another process won the race).
func writeTokenSecretExclusive(path string, data []byte, perm os.FileMode) error {
	// Write full content to a unique temp file in the same directory.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".token_secret.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = fileutil.Remove(tmpPath) }() // Clean up temp file regardless.

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Hard-link is atomic and fails if target exists, preventing race conditions.
	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
