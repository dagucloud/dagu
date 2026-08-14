// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
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

func resolvePersistedTokenSecret(authDir string) (auth.TokenSecret, error) {
	path := filepath.Join(authDir, tokenSecretFileName)

	fileExists := false
	data, err := fileutil.ReadFile(path)
	if err == nil {
		fileExists = true
		content := strings.TrimSpace(string(data))
		if content != "" {
			return auth.NewTokenSecretFromString(content)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return auth.TokenSecret{}, fmt.Errorf("failed to read token secret file %s: %w", path, err)
	}

	secret, err := generateTokenSecret()
	if err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to generate token secret: %w", err)
	}

	if err := os.MkdirAll(authDir, tokenSecretDirPerm); err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to create auth directory %s: %w", authDir, err)
	}
	if err := os.Chmod(authDir, tokenSecretDirPerm); err != nil {
		return auth.TokenSecret{}, fmt.Errorf("failed to set auth directory permissions %s: %w", authDir, err)
	}

	if fileExists {
		if err := fileutil.Remove(path); err != nil {
			return auth.TokenSecret{}, fmt.Errorf("failed to remove empty token secret file %s: %w", path, err)
		}
	}

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

func writeTokenSecretExclusive(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".token_secret.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = fileutil.Remove(tmpPath) }()

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

	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
