// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	artifactsSubdir  = "browser"
	downloadsSubdir  = "downloads"
	artifactDirMode  = 0o755
	artifactFileMode = 0o644
)

var errNoArtifactStorage = errors.New("browser: screenshots need artifact storage, which is disabled for this DAG")

// artifactStore writes a step's files under the run's artifacts directory.
type artifactStore struct {
	// root is the run artifacts directory; empty when storage is disabled.
	root string
	// rel is the step directory relative to root, using forward slashes.
	rel      string
	sequence int
}

func newArtifactStore(root, stepKey string) *artifactStore {
	return &artifactStore{root: root, rel: path.Join(artifactsSubdir, fileutil.SafeName(stepKey))}
}

func (s *artifactStore) enabled() bool {
	return s.root != ""
}

// downloadsDir returns where the browser saves downloads, or "" when
// artifact storage is disabled.
func (s *artifactStore) downloadsDir() (string, error) {
	if !s.enabled() {
		return "", nil
	}
	dir := filepath.Join(s.root, filepath.FromSlash(s.rel), downloadsSubdir)
	if err := os.MkdirAll(dir, artifactDirMode); err != nil {
		return "", fmt.Errorf("create downloads directory: %w", err)
	}
	return dir, nil
}

// writeScreenshot stores a PNG and returns its path relative to the run
// artifacts directory.
func (s *artifactStore) writeScreenshot(label string, data []byte) (string, error) {
	if !s.enabled() {
		return "", errNoArtifactStorage
	}
	s.sequence++
	rel := path.Join(s.rel, fmt.Sprintf("%02d-%s.png", s.sequence, fileutil.SafeName(label)))
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), artifactDirMode); err != nil {
		return "", fmt.Errorf("create screenshot directory: %w", err)
	}
	if err := os.WriteFile(full, data, artifactFileMode); err != nil {
		return "", fmt.Errorf("write screenshot: %w", err)
	}
	return rel, nil
}
