// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

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
	artifactsSubdir  = "browser"
	downloadsSubdir  = "downloads"
	artifactDirMode  = 0o755
	artifactFileMode = 0o644
	screenshotExt    = ".png"
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

// downloadPath returns where a finished download is stored, relative to the
// run artifacts directory.
func (s *artifactStore) downloadPath(name string) string {
	return path.Join(s.rel, downloadsSubdir, name)
}

// writeScreenshot stores a PNG and returns its path relative to the run
// artifacts directory. Screenshots from earlier executions of the step, such
// as a retry or the part before an ask, are kept.
func (s *artifactStore) writeScreenshot(label string, data []byte) (string, error) {
	if !s.enabled() {
		return "", errNoArtifactStorage
	}
	dir := filepath.Join(s.root, filepath.FromSlash(s.rel))
	if err := os.MkdirAll(dir, artifactDirMode); err != nil {
		return "", fmt.Errorf("create screenshot directory: %w", err)
	}
	if s.sequence == 0 {
		s.sequence = lastScreenshotSequence(dir)
	}
	for {
		s.sequence++
		name := fmt.Sprintf("%02d-%s%s", s.sequence, fileutil.SafeName(label), screenshotExt)
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, artifactFileMode)
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
