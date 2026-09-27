// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	artifactDirMode  = 0o755
	artifactFileMode = 0o644
	screenshotExt    = ".png"
)

// ErrNoArtifactStorage reports a screenshot in a DAG with artifacts
// disabled.
var ErrNoArtifactStorage = errors.New("screenshots need artifact storage, which is disabled for this DAG")

// ArtifactStore writes a step's files under the run's artifacts directory.
type ArtifactStore struct {
	// root is the run artifacts directory; empty when storage is disabled.
	root string
	// rel is the step directory relative to root, using forward slashes.
	rel      string
	sequence int
}

// NewArtifactStore returns the store of a step, whose files go under
// subdir/<step key> in the run artifacts directory root. An empty root
// means storage is disabled.
func NewArtifactStore(root, subdir, stepKey string) *ArtifactStore {
	return &ArtifactStore{root: root, rel: path.Join(subdir, fileutil.SafeName(stepKey))}
}

// Enabled reports whether the DAG stores artifacts.
func (s *ArtifactStore) Enabled() bool {
	return s.root != ""
}

// Dir returns the step's directory on disk.
func (s *ArtifactStore) Dir() string {
	return filepath.Join(s.root, filepath.FromSlash(s.rel))
}

// RelPath returns a path under the step's directory, relative to the run
// artifacts directory.
func (s *ArtifactStore) RelPath(elem ...string) string {
	return path.Join(append([]string{s.rel}, elem...)...)
}

// WriteScreenshot stores a PNG and returns its path relative to the run
// artifacts directory. Screenshots from earlier executions of the step, such
// as a retry or the part before an ask, are kept.
func (s *ArtifactStore) WriteScreenshot(label string, data []byte) (string, error) {
	if !s.Enabled() {
		return "", ErrNoArtifactStorage
	}
	dir := s.Dir()
	if err := os.MkdirAll(dir, artifactDirMode); err != nil {
		return "", fmt.Errorf("create screenshot directory: %w", err)
	}
	if s.sequence == 0 {
		s.sequence = lastScreenshotSequence(dir)
	}
	for {
		s.sequence++
		name := fmt.Sprintf("%02d-%s%s", s.sequence, fileutil.SafeName(label), screenshotExt)
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, artifactFileMode) //nolint:gosec // name is built from a sanitized label
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("write screenshot: %w", err)
		}
		_, err = file.Write(data)
		if err := errors.Join(err, file.Close()); err != nil {
			return "", fmt.Errorf("write screenshot: %w", err)
		}
		return path.Join(s.rel, name), nil
	}
}

// lastScreenshotSequence returns the highest sequence number among the
// screenshots in dir, so numbering continues across step executions.
func lastScreenshotSequence(dir string) int {
	entries, _ := os.ReadDir(dir)
	last := 0
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "-")
		if entry.IsDir() || !ok || filepath.Ext(entry.Name()) != screenshotExt {
			continue
		}
		if sequence, err := strconv.Atoi(prefix); err == nil && sequence > last {
			last = sequence
		}
	}
	return last
}
