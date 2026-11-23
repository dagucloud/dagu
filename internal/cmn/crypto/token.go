// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const tokenRandomBytes = 32

// ReadToken returns the token stored in path without surrounding whitespace.
// An empty file is an error.
func ReadToken(path string) (string, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return token, nil
}

// LoadOrCreateToken returns the token stored in path, first generating a
// random hex token and storing it when the file does not exist. Processes that
// share the file get the same token, since only one of them can create it.
func LoadOrCreateToken(path string) (string, error) {
	token, err := ReadToken(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}

	raw := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token = hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create token directory: %w", err)
	}
	err = fileutil.WriteFileAtomicExclusive(path, []byte(token+"\n"), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ReadToken(path)
	}
	if err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	return token, nil
}
