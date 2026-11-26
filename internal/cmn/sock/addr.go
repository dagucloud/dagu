// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"crypto/md5" //nolint:gosec
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	hashLength = 6
	// maxSocketNameLength keeps the whole address within the 104 bytes that
	// macOS allows for a Unix socket path.
	maxSocketNameLength = 50
	prefix              = "@dagu_"
	suffix              = ".sock"
)

// Addr returns the local socket address for an identity and run ID of the
// home whose data lives in dataDir. Two homes on one host never share an
// address, even for the same identity and run ID.
func Addr(dataDir, identity, runID string) string {
	return addr(identity, runID, "_"+hashOf(dataDirKey(dataDir)))
}

// LegacyAddr returns the address that earlier versions used for an identity
// and run ID. It names no data directory, so it serves only to reach a run
// that such a version started.
func LegacyAddr(identity, runID string) string {
	return addr(identity, runID, "")
}

// addr builds an address whose name ends with the hash of the identity and
// the run ID, after scope.
func addr(identity, runID, scope string) string {
	hash := scope + "_" + hashOf(identity+runID)
	safeName := fileutil.SafeName(identity)
	// The name sits between a prefix and a hash, so a Windows device name is
	// no device here, and it keeps the address that earlier versions used.
	if fileutil.SafeNameEscapesDevice(identity) {
		safeName = identity
	}
	maxNameLen := maxSocketNameLength - len(prefix) - len(hash) - len(suffix)
	if len(safeName) > maxNameLen {
		safeName = safeName[:maxNameLen]
	}

	return filepath.Join(socketDir(), prefix+safeName+hash+suffix)
}

func hashOf(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))[:hashLength] //nolint:gosec
}

// dataDirKey gives one key for every spelling of a data directory that the
// processes of one home can use.
func dataDirKey(dataDir string) string {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	dataDir = filepath.Clean(dataDir)
	if runtime.GOOS == "windows" {
		dataDir = strings.ToLower(dataDir)
	}
	return dataDir
}

func socketDir() string {
	if runtime.GOOS == "windows" {
		return os.TempDir()
	}
	return "/tmp"
}
