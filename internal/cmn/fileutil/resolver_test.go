// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileResolver(t *testing.T) {
	// Create temporary test directories
	tempDir := t.TempDir()
	dir1 := filepath.Join(tempDir, "dir1")
	dir2 := filepath.Join(tempDir, "dir2")

	// Create test directories
	for _, dir := range []string{dir1, dir2} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
	}

	// Create test files
	testFiles := map[string]string{
		filepath.Join(dir1, "file1.txt"):            "content1",
		filepath.Join(dir2, "file2.txt"):            "content2",
		filepath.Join(dir1, "shared.txt"):           "content_dir1",
		filepath.Join(dir2, "shared.txt"):           "content_dir2",
		filepath.Join(tempDir, "absolute.txt"):      "absolute",
		filepath.Join(os.TempDir(), "homefile.txt"): "home",
	}

	for path, content := range testFiles {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	tests := []struct {
		name          string
		relativeTos   []string
		file          string
		expectedPath  string
		expectedError bool
	}{
		{
			name:         "FindFileInFirstRelativePath",
			relativeTos:  []string{dir1, dir2},
			file:         "file1.txt",
			expectedPath: filepath.Join(dir1, "file1.txt"),
		},
		{
			name:         "FindFileInSecondRelativePath",
			relativeTos:  []string{dir1, dir2},
			file:         "file2.txt",
			expectedPath: filepath.Join(dir2, "file2.txt"),
		},
		{
			name:         "FindSharedFileShouldUseFirstMatch",
			relativeTos:  []string{dir1, dir2},
			file:         "shared.txt",
			expectedPath: filepath.Join(dir1, "shared.txt"),
		},
		{
			name:         "AbsolutePathExists",
			relativeTos:  []string{dir1, dir2},
			file:         filepath.Join(tempDir, "absolute.txt"),
			expectedPath: filepath.Join(tempDir, "absolute.txt"),
		},
		{
			name:          "AbsolutePathDoesNotExist",
			relativeTos:   []string{dir1, dir2},
			file:          filepath.Join(tempDir, "nonexistent.txt"),
			expectedError: true,
		},
		{
			name:          "FileNotFoundInAnyLocation",
			relativeTos:   []string{dir1, dir2},
			file:          "nonexistent.txt",
			expectedError: true,
		},
		{
			name:          "EmptyRelativePaths",
			relativeTos:   []string{},
			file:          "file1.txt",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewFileResolver(tt.relativeTos)
			path, err := resolver.ResolveFilePath(tt.file)

			// Check error cases
			if tt.expectedError {
				if err == nil {
					t.Error("expected error but got none")
					return
				}
				return
			}

			// Check success cases
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if path != tt.expectedPath {
				t.Errorf("expected path %s but got %s", tt.expectedPath, path)
			}

			// Verify the file exists
			if !FileExists(path) {
				t.Errorf("resolved path %s does not exist", path)
			}
		})
	}
}

func TestFileResolverHomeTilde(t *testing.T) {
	homeDir := t.TempDir()
	target := filepath.Join(homeDir, "dotenv")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	t.Setenv("HOME", homeDir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", homeDir)
	}

	resolver := NewFileResolver(nil)

	resolved, err := resolver.ResolveFilePath("~/dotenv")
	if err != nil {
		t.Fatalf("unexpected error resolving ~/ path: %v", err)
	}
	if resolved != target {
		t.Errorf("expected %s, got %s", target, resolved)
	}
}

func TestFileResolverUserTildeError(t *testing.T) {
	// A ~name path cannot be resolved portably; the resolution error must
	// surface instead of collapsing into FileNotFoundError.
	resolver := NewFileResolver(nil)

	assertTildeUserError := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected error for ~name path, got none")
		}
		if _, ok := errors.AsType[*FileNotFoundError](err); ok {
			t.Fatalf("expected resolution error, got FileNotFoundError: %v", err)
		}
		if !strings.Contains(err.Error(), "~alice/missing.txt") {
			t.Errorf("error should mention the offending path, got: %v", err)
		}
	}

	for _, resolve := range []func(string) (string, error){
		resolver.ResolveFilePath,
		resolver.ResolveFilePathLiteral,
	} {
		// Leading whitespace must not push a ~name path into search mode.
		for _, path := range []string{"~alice/missing.txt", " ~alice/missing.txt"} {
			_, err := resolve(path)
			assertTildeUserError(t, err)
		}
	}

	// An environment variable that expands to a ~name path hits the same
	// rejection; the literal resolver never expands it.
	t.Setenv("TILDE_USER", "~alice")
	_, err := resolver.ResolveFilePath("$TILDE_USER/missing.txt")
	assertTildeUserError(t, err)
}

func TestFileNotFoundError(t *testing.T) {
	tests := []struct {
		name        string
		err         *FileNotFoundError
		expectedMsg string
	}{
		{
			name: "ErrorWithNoSearchedPaths",
			err: &FileNotFoundError{
				Path: "test.txt",
			},
			expectedMsg: "file not found: test.txt",
		},
		{
			name: "ErrorWithSearchedPaths",
			err: &FileNotFoundError{
				Path:          "test.txt",
				SearchedPaths: []string{"/path1", "/path2"},
			},
			expectedMsg: "file not found: test.txt (searched in: [/path1 /path2])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if msg != tt.expectedMsg {
				t.Errorf("expected message %q but got %q", tt.expectedMsg, msg)
			}
		})
	}
}
