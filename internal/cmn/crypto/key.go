// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	keyFileName    = "encryption_key"
	keyDirName     = "auth"
	keyFilePerms   = os.FileMode(0600)
	keyDirPerms    = os.FileMode(0750)
	keyRandomBytes = 32
)

// ResolveKey returns an encryption key using the following priority:
// 1. DAGU_ENCRYPTION_KEY environment variable
// 2. Key file at <dataDir>/auth/encryption_key
// 3. Auto-generates and persists a new random key
func ResolveKey(dataDir string) (string, error) {
	// 1. Environment variable
	if key := os.Getenv("DAGU_ENCRYPTION_KEY"); key != "" {
		return key, nil
	}

	// 2. File-based key
	keyDir := filepath.Join(dataDir, keyDirName)
	keyPath := filepath.Join(keyDir, keyFileName)

	data, err := os.ReadFile(keyPath) //nolint:gosec // path is constructed from trusted dataDir
	if err == nil {
		key := string(data)
		if key != "" {
			return key, nil
		}
	}

	// 3. Auto-generate
	rawKey := make([]byte, keyRandomBytes)
	if _, err := rand.Read(rawKey); err != nil {
		return "", fmt.Errorf("crypto: failed to generate random key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(rawKey)

	if err := os.MkdirAll(keyDir, keyDirPerms); err != nil {
		return "", fmt.Errorf("crypto: failed to create key directory: %w", err)
	}

	if err := fileutil.WriteFileAtomic(keyPath, []byte(key), keyFilePerms); err != nil {
		return "", fmt.Errorf("crypto: failed to persist encryption key: %w", err)
	}

	return key, nil
}
