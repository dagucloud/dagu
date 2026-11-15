// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// runBinaryPrefix starts the name of each copy copyRunBinary makes.
const runBinaryPrefix = "dagu-"

// copyRunBinary returns a copy of executable in dir, made once for each
// build of executable, for launched runs to execute. A process keeps the
// binary it runs in use, and Windows lets such a file be renamed but not
// replaced or removed, so runs that outlive the process launching them
// would otherwise keep the installed binary from being upgraded or removed.
// Copies of other builds are removed once nothing runs them.
func copyRunBinary(executable, dir string) (string, error) {
	info, err := fileutil.Stat(executable)
	if err != nil {
		return "", err
	}
	// The size and modification time tell builds apart without reading the
	// whole binary.
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%d", info.Size(), info.ModTime().UnixNano()))
	name := runBinaryPrefix + hex.EncodeToString(sum[:8]) + filepath.Ext(executable)
	target := filepath.Join(dir, name)

	if existing, err := fileutil.Stat(target); err != nil || existing.Size() != info.Size() {
		if err := fileutil.MkdirAll(dir, 0o750); err != nil {
			return "", err
		}
		if err := copyFileAtomic(executable, target, info.Mode().Perm()); err != nil {
			return "", err
		}
	}
	removeOtherRunBinaries(dir, name)
	return target, nil
}

// copyFileAtomic copies source to target through a temporary file in the
// target's directory, so target is either absent or complete.
func copyFileAtomic(source, target string, perm os.FileMode) error {
	in, err := fileutil.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	temp := out.Name()
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temp, perm)
	}
	if err == nil {
		err = fileutil.Rename(temp, target)
	}
	if err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("copy %s to %s: %w", source, target, err)
	}
	return nil
}

// removeOtherRunBinaries removes the copies in dir other than keep. A copy
// a run still executes cannot be removed, so each is tried once and left
// for a later call.
func removeOtherRunBinaries(dir, keep string) {
	entries, err := fileutil.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if name := entry.Name(); name != keep && strings.HasPrefix(name, runBinaryPrefix) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
