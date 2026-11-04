// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workpath

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "data.json")
	tests := []struct {
		name    string
		workDir string
		path    string
		want    string
	}{
		{"relative", workDir, filepath.Join("in", "data.json"), filepath.Join(workDir, "in", "data.json")},
		{"dot relative", workDir, "./data.json", filepath.Join(workDir, "data.json")},
		{"absolute", workDir, absolute, absolute},
		// On Windows a leading separator is rooted at the current drive.
		{"rooted", workDir, "/data/in.json", "/data/in.json"},
		{"empty path", workDir, "", ""},
		{"no working directory", "", "data.json", "data.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Resolve(tt.workDir, tt.path))
		})
	}
}
