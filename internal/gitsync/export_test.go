// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

import "github.com/go-git/go-git/v5/plumbing/transport"

func AuthForTest(cfg *Config) (transport.AuthMethod, error) {
	return NewGitClient(cfg, "").getAuth()
}

func NormalizeRepoURLForTest(repo string) string {
	return NewGitClient(&Config{Repository: repo}, "").normalizeRepoURL()
}

func IsSCPStyleURLForTest(repo string) bool {
	return isSCPStyleURL(repo)
}

func SafeReadFileWithinBaseForTest(baseDir, targetPath string) ([]byte, error) {
	return safeReadFileWithinBase(baseDir, targetPath)
}

func SafeWriteFileWithinBaseForTest(baseDir, targetPath string, content []byte) error {
	return safeWriteFileWithinBase(baseDir, targetPath, content, 0600)
}
