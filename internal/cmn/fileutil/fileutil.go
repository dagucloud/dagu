// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Common errors for file operations
var (
	ErrUnexpectedEOF         = errors.New("unexpected end of input after escape character")
	ErrUnknownEscapeSequence = errors.New("unknown escape sequence")
)

// MustGetUserHomeDir returns user home directory.
// Returns empty string if os.UserHomeDir() fails.
func MustGetUserHomeDir() string {
	hd, _ := os.UserHomeDir()
	return hd
}

// MustGetwd returns current working directory.
// Returns empty string if os.Getwd() fails.
func MustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// IsDir returns true if path is a directory.
func IsDir(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// FileExists reports whether the named file exists.
// It returns false if os.Stat reports the file does not exist and true otherwise (including when os.Stat returns a different error).
func FileExists(file string) bool {
	_, err := os.Stat(file)
	return !os.IsNotExist(err)
}

// IsFile reports whether the named path exists and is a regular file.
// It returns false if the path does not exist or if an error occurs while obtaining file info.
func IsFile(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.Mode().IsRegular()
}

// OpenOrCreateFile opens or creates the named file for appending with synchronous I/O and sets permissions to 0600.
// It returns the opened *os.File or a non-nil error if the operation fails.
func OpenOrCreateFile(filepath string) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND | os.O_SYNC

	var file *os.File
	err := retryWindowsFileOp(func() error {
		opened, err := os.OpenFile(filepath, flags, 0600) // nolint:gosec
		if err != nil {
			return err
		}
		file = opened
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create/open log file %s: %w", filepath, err)
	}

	return file, nil
}

// MustTempDir returns temporary directory.
// This function is used only for testing.
func MustTempDir(pattern string) string {
	t, err := os.MkdirTemp("", pattern)
	if err != nil {
		panic(err)
	}
	return t
}

// TruncString truncates string to max length.
func TruncString(val string, max int) string {
	if len(val) > max {
		return val[:max]
	}
	return val
}

const (
	yamlExtension = ".yaml"
	ymlExtension  = ".yml"
)

// ValidYAMLExtensions contains valid YAML extensions.
var ValidYAMLExtensions = []string{yamlExtension, ymlExtension}

// IsYAMLFile checks if a file has a valid YAML extension (.yaml or .yml).
// Returns false for empty strings or files without extensions.
func IsYAMLFile(filename string) bool {
	if filename == "" {
		return false
	}
	return slices.Contains(ValidYAMLExtensions, filepath.Ext(filename))
}

// TrimYAMLFileExtension trims the .yml or .yaml extension from a filename.
func TrimYAMLFileExtension(filename string) string {
	if filename == "" {
		return ""
	}

	ext := filepath.Ext(filename)
	switch ext {
	case ymlExtension:
		return strings.TrimSuffix(filename, ymlExtension)
	case yamlExtension:
		return strings.TrimSuffix(filename, yamlExtension)
	default:
		return filename
	}
}

// IsFileWithExtension is a more generic function that checks if a file
// has any of the provided extensions.
func IsFileWithExtension(filename string, validExtensions []string) bool {
	if filename == "" || len(validExtensions) == 0 {
		return false
	}
	return slices.Contains(validExtensions, filepath.Ext(filename))
}

// EnsureYAMLExtension adds .yaml extension if not present
// if it has .yml extension, replace it with .yaml
func EnsureYAMLExtension(filename string) string {
	if filename == "" {
		return ""
	}

	ext := filepath.Ext(filename)
	switch ext {
	case ymlExtension, yamlExtension:
		return filename

	default:
		return filename + yamlExtension
	}
}

// ResolvePath resolves a path to an absolute path.
// It handles empty paths, tilde expansion, environment variables,
// and converts to an absolute path.
func ResolvePath(path string) (string, error) {
	path = strings.TrimSpace(path)

	// Handle empty path case
	if path == "" {
		return "", nil
	}

	// Expand environment variables
	path = os.ExpandEnv(path)

	// Expand tilde to user's home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Clean the path
	cleanPath := filepath.Clean(absPath)

	return cleanPath, nil
}

// ResolvePathOrBlank works like ResolvePath but returns original path on error.
// Useful when you're confident the path resolution will succeed.
func ResolvePathOrBlank(path string) string {
	resolvedPath, err := ResolvePath(path)
	if err != nil {
		log.Println("Failed to resolve path:", err)
		return path
	}
	return resolvedPath
}

// CreateTempDAGFile creates a temporary file with DAG YAML content.
// The file is created in {os.TempDir()}/dagu/{subDir}/ with pattern {dagName}-*.yaml.
// Additional YAML documents can be appended by providing extraDocs.
// Returns the path to the created file or an error.
func CreateTempDAGFile(subDir, dagName string, yamlData []byte, extraDocs ...[]byte) (string, error) {
	// Create a temporary directory if it doesn't exist
	tempDir := filepath.Join(os.TempDir(), "dagu", subDir)
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Cap prefix so basename (without .yaml) stays within the 40-char DAG name
	// limit. os.CreateTemp replaces '*' with up to 10 random digits, plus the
	// '-' separator we add = 11 chars of overhead.
	const maxTempPrefix = 29 // 40 (DAGNameMaxLen) - 11 (separator + random suffix)
	patternName := strings.TrimSpace(dagName)
	if patternName != "" {
		patternName = filepath.Base(patternName)
		if ext := strings.ToLower(filepath.Ext(patternName)); ext == ".yaml" || ext == ".yml" {
			patternName = strings.TrimSuffix(patternName, filepath.Ext(patternName))
		}
	}
	if patternName == "" {
		patternName = "dag"
	}
	pattern := fmt.Sprintf("%s-*.yaml", TruncString(patternName, maxTempPrefix))
	tempFile, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}

	tempFileName := tempFile.Name()
	writeErr := func() error {
		defer func() { _ = tempFile.Close() }()

		// Write the primary YAML data
		if _, err := tempFile.Write(yamlData); err != nil {
			return err
		}

		// Write additional YAML documents if provided
		lastData := yamlData
		for _, doc := range extraDocs {
			// Ensure previous data ends with newline before adding separator
			// YAML spec requires document separator to start on its own line
			if len(lastData) > 0 && lastData[len(lastData)-1] != '\n' {
				if _, err := tempFile.WriteString("\n"); err != nil {
					return err
				}
			}
			if _, err := tempFile.WriteString("---\n"); err != nil {
				return err
			}
			if _, err := tempFile.Write(doc); err != nil {
				return err
			}
			lastData = doc
		}
		return nil
	}()

	if writeErr != nil {
		_ = os.Remove(tempFileName)
		return "", fmt.Errorf("failed to write YAML data: %w", writeErr)
	}

	return tempFileName, nil
}

// WriteFileAtomic writes data to a file atomically using a temp file and rename.
// This ensures the file is never left in a partial state.
// Uses os.CreateTemp with a unique filename to prevent race conditions with
// concurrent writers to the same file.
func WriteFileAtomic(filePath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)

	// Create temp file in the same directory to ensure atomic rename works
	// (rename across filesystems would fail)
	tempFile, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tempPath := tempFile.Name()

	// Clean up temp file on any error
	cleanup := func() { _ = os.Remove(tempPath) }

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		cleanup()
		return fmt.Errorf("failed to write temp file %s: %w", tempPath, err)
	}

	if err := tempFile.Chmod(perm); err != nil {
		_ = tempFile.Close()
		cleanup()
		return fmt.Errorf("failed to set permissions on temp file %s: %w", tempPath, err)
	}

	if err := tempFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file %s: %w", tempPath, err)
	}

	if err := ReplaceFileWithRetry(tempPath, filePath); err != nil {
		cleanup()
		return fmt.Errorf("failed to rename %s to %s: %w", tempPath, filePath, err)
	}
	return nil
}

// WriteJSONAtomic marshals v to indented JSON and writes it atomically to filePath.
func WriteJSONAtomic(filePath string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return WriteFileAtomic(filePath, data, perm)
}
