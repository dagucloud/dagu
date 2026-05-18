// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

var _ CacheStore = (*mockCacheStore)(nil)

func testBinaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func createVersionTestBinary(t testing.TB, dir, baseName, version string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		scriptPath := filepath.Join(dir, baseName+".cmd")
		require.NoError(t, os.WriteFile(
			scriptPath,
			fmt.Appendf(nil, "@echo off\r\necho dagu version %s\r\n", version),
			0755,
		))
		return scriptPath
	}

	scriptPath := filepath.Join(dir, baseName)
	require.NoError(t, os.WriteFile(
		scriptPath,
		fmt.Appendf(nil, "#!/bin/sh\necho 'dagu version %s'\n", version),
		0755,
	))
	return scriptPath
}

func assertPermsIfSupported(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		return
	}
	if info.Mode().Perm() != want {
		t.Errorf("%s permissions = %o, want %o", path, info.Mode().Perm(), want)
	}
}

// mockCacheStore implements CacheStore for testing.
type mockCacheStore struct {
	cache *UpgradeCheckCache
	err   error
}

func (m *mockCacheStore) Load() (*UpgradeCheckCache, error) {
	return m.cache, m.err
}

func (m *mockCacheStore) Save(cache *UpgradeCheckCache) error {
	m.cache = cache
	return m.err
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantMajor int64
		wantMinor int64
		wantPatch int64
	}{
		{
			name:      "version with v prefix",
			input:     "v1.30.3",
			wantMajor: 1,
			wantMinor: 30,
			wantPatch: 3,
		},
		{
			name:      "version without v prefix",
			input:     "1.30.3",
			wantMajor: 1,
			wantMinor: 30,
			wantPatch: 3,
		},
		{
			name:      "version with prerelease",
			input:     "v1.30.0-rc.1",
			wantMajor: 1,
			wantMinor: 30,
			wantPatch: 0,
		},
		{
			name:      "version with build timestamp",
			input:     "v1.30.3-260204123456",
			wantMajor: 1,
			wantMinor: 30,
			wantPatch: 3,
		},
		{name: "development version", input: "dev", wantErr: true},
		{name: "zero version", input: "0.0.0", wantErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "invalid format", input: "not-a-version", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseVersion(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseVersion(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseVersion(%q) unexpected error: %v", tt.input, err)
				return
			}
			if v.Major() != uint64(tt.wantMajor) {
				t.Errorf("ParseVersion(%q) major = %d, want %d", tt.input, v.Major(), tt.wantMajor)
			}
			if v.Minor() != uint64(tt.wantMinor) {
				t.Errorf("ParseVersion(%q) minor = %d, want %d", tt.input, v.Minor(), tt.wantMinor)
			}
			if v.Patch() != uint64(tt.wantPatch) {
				t.Errorf("ParseVersion(%q) patch = %d, want %d", tt.input, v.Patch(), tt.wantPatch)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    int
		isNewer bool
	}{
		{name: "target is newer", current: "v1.30.0", target: "v1.30.3", want: -1, isNewer: true},
		{name: "versions are equal", current: "v1.30.3", target: "v1.30.3", want: 0, isNewer: false},
		{name: "current is newer", current: "v1.31.0", target: "v1.30.3", want: 1, isNewer: false},
		{name: "major version difference", current: "v1.30.3", target: "v2.0.0", want: -1, isNewer: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, err := ParseVersion(tt.current)
			if err != nil {
				t.Fatalf("ParseVersion(%q) failed: %v", tt.current, err)
			}
			target, err := ParseVersion(tt.target)
			if err != nil {
				t.Fatalf("ParseVersion(%q) failed: %v", tt.target, err)
			}

			got := CompareVersions(current, target)
			if got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.current, tt.target, got, tt.want)
			}

			gotNewer := IsNewer(current, target)
			if gotNewer != tt.isNewer {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.target, gotNewer, tt.isNewer)
			}
		})
	}
}

func TestPlatformDetect(t *testing.T) {
	p := Detect()
	if p.OS == "" {
		t.Error("Detect() returned empty OS")
	}
	if p.Arch == "" {
		t.Error("Detect() returned empty Arch")
	}
}

func TestPlatformAssetName(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		version  string
		want     string
	}{
		{
			name:     "darwin arm64 with v prefix",
			platform: Platform{OS: "darwin", Arch: "arm64"},
			version:  "v1.30.3",
			want:     "dagu_1.30.3_darwin_arm64.tar.gz",
		},
		{
			name:     "linux amd64 without v prefix",
			platform: Platform{OS: "linux", Arch: "amd64"},
			version:  "1.30.3",
			want:     "dagu_1.30.3_linux_amd64.tar.gz",
		},
		{
			name:     "windows 386",
			platform: Platform{OS: "windows", Arch: "386"},
			version:  "v2.0.0",
			want:     "dagu_2.0.0_windows_386.tar.gz",
		},
		{
			name:     "linux armv7",
			platform: Platform{OS: "linux", Arch: "armv7"},
			version:  "v1.30.3",
			want:     "dagu_1.30.3_linux_armv7.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.platform.AssetName(tt.version)
			if got != tt.want {
				t.Errorf("Platform{%s, %s}.AssetName(%q) = %q, want %q",
					tt.platform.OS, tt.platform.Arch, tt.version, got, tt.want)
			}
		})
	}
}

func TestPlatformIsSupported(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		want     bool
	}{
		{name: "darwin arm64", platform: Platform{OS: "darwin", Arch: "arm64"}, want: true},
		{name: "darwin amd64", platform: Platform{OS: "darwin", Arch: "amd64"}, want: true},
		{name: "linux amd64", platform: Platform{OS: "linux", Arch: "amd64"}, want: true},
		{name: "linux arm64", platform: Platform{OS: "linux", Arch: "arm64"}, want: true},
		{name: "linux 386", platform: Platform{OS: "linux", Arch: "386"}, want: true},
		{name: "windows amd64", platform: Platform{OS: "windows", Arch: "amd64"}, want: true},
		{name: "freebsd amd64", platform: Platform{OS: "freebsd", Arch: "amd64"}, want: true},
		{name: "unsupported os", platform: Platform{OS: "plan9", Arch: "amd64"}, want: false},
		{name: "unsupported arch", platform: Platform{OS: "darwin", Arch: "mips"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.platform.IsSupported()
			if got != tt.want {
				t.Errorf("Platform{%s, %s}.IsSupported() = %v, want %v",
					tt.platform.OS, tt.platform.Arch, got, tt.want)
			}
		})
	}
}

func TestPlatformString(t *testing.T) {
	tests := []struct {
		platform Platform
		want     string
	}{
		{Platform{OS: "darwin", Arch: "arm64"}, "darwin/arm64"},
		{Platform{OS: "linux", Arch: "amd64"}, "linux/amd64"},
		{Platform{OS: "windows", Arch: "386"}, "windows/386"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.platform.String()
			if got != tt.want {
				t.Errorf("Platform{%s, %s}.String() = %q, want %q", tt.platform.OS, tt.platform.Arch, got, tt.want)
			}
		})
	}
}

func TestSupportedPlatformsMessage(t *testing.T) {
	msg := SupportedPlatformsMessage()

	expectedPlatforms := []string{"darwin", "linux", "freebsd", "openbsd", "windows"}
	for _, p := range expectedPlatforms {
		if !strings.Contains(msg, p) {
			t.Errorf("SupportedPlatformsMessage() should contain %q", p)
		}
	}

	expectedArches := []string{"amd64", "arm64", "386"}
	for _, a := range expectedArches {
		if !strings.Contains(msg, a) {
			t.Errorf("SupportedPlatformsMessage() should contain %q", a)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "standard sha256sum format (two spaces)",
			content: `abc123def456  dagu_1.30.3_darwin_arm64.tar.gz
789xyz012345  dagu_1.30.3_linux_amd64.tar.gz`,
			want: map[string]string{
				"dagu_1.30.3_darwin_arm64.tar.gz": "abc123def456",
				"dagu_1.30.3_linux_amd64.tar.gz":  "789xyz012345",
			},
		},
		{
			name: "with extra whitespace",
			content: `  abc123def456  dagu_1.30.3_darwin_arm64.tar.gz
789xyz012345  dagu_1.30.3_linux_amd64.tar.gz`,
			want: map[string]string{
				"dagu_1.30.3_darwin_arm64.tar.gz": "abc123def456",
				"dagu_1.30.3_linux_amd64.tar.gz":  "789xyz012345",
			},
		},
		{
			name:    "single space fallback",
			content: `abc123def456 dagu_1.30.3_darwin_arm64.tar.gz`,
			want: map[string]string{
				"dagu_1.30.3_darwin_arm64.tar.gz": "abc123def456",
			},
		},
		{name: "empty content", content: "", wantErr: true},
		{name: "whitespace only", content: "   \n   \n   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChecksums(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseChecksums() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("parseChecksums() unexpected error: %v", err)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("parseChecksums() got %d entries, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("parseChecksums()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestNormalizeVersionTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.30.3", "v1.30.3"},
		{"v1.30.3", "v1.30.3"},
		{"  v1.30.3  ", "v1.30.3"},
		{"  1.30.3  ", "v1.30.3"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeVersionTag(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeVersionTag(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractVersionFromTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.30.3", "1.30.3"},
		{"1.30.3", "1.30.3"},
		{"v2.0.0-rc.1", "2.0.0-rc.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExtractVersionFromTag(tt.input)
			if got != tt.want {
				t.Errorf("ExtractVersionFromTag(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindAsset(t *testing.T) {
	release := &Release{
		TagName: "v1.30.3",
		Assets: []Asset{
			{Name: "dagu_1.30.3_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64"},
			{Name: "dagu_1.30.3_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
		},
	}

	tests := []struct {
		name     string
		platform Platform
		version  string
		wantName string
		wantErr  bool
	}{
		{
			name:     "darwin arm64 found",
			platform: Platform{OS: "darwin", Arch: "arm64"},
			version:  "v1.30.3",
			wantName: "dagu_1.30.3_darwin_arm64.tar.gz",
		},
		{
			name:     "linux amd64 found",
			platform: Platform{OS: "linux", Arch: "amd64"},
			version:  "v1.30.3",
			wantName: "dagu_1.30.3_linux_amd64.tar.gz",
		},
		{
			name:     "platform not found",
			platform: Platform{OS: "windows", Arch: "amd64"},
			version:  "v1.30.3",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset, err := FindAsset(release, tt.platform, tt.version)
			if tt.wantErr {
				if err == nil {
					t.Errorf("FindAsset() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("FindAsset() unexpected error: %v", err)
				return
			}
			if asset.Name != tt.wantName {
				t.Errorf("FindAsset() name = %q, want %q", asset.Name, tt.wantName)
			}
		})
	}
}

func TestDetectInstallMethod(t *testing.T) {
	method := DetectInstallMethod()

	if method.String() == "" {
		t.Error("DetectInstallMethod() returned empty string")
	}

	validMethods := map[InstallMethod]bool{
		InstallMethodBinary:    true,
		InstallMethodGoInstall: true,
		InstallMethodHomebrew:  true,
		InstallMethodSnap:      true,
		InstallMethodDocker:    true,
		InstallMethodUnknown:   true,
	}
	if !validMethods[method] {
		t.Errorf("DetectInstallMethod() returned unknown method: %v", method)
	}

	knownStrings := []string{"binary", "go install", "homebrew", "snap", "docker", "unknown"}
	if !slices.Contains(knownStrings, method.String()) {
		t.Errorf("DetectInstallMethod().String() returned unexpected value: %q", method.String())
	}
}

func TestInstallMethodString(t *testing.T) {
	tests := []struct {
		method InstallMethod
		want   string
	}{
		{InstallMethodUnknown, "unknown"},
		{InstallMethodBinary, "binary"},
		{InstallMethodHomebrew, "homebrew"},
		{InstallMethodSnap, "snap"},
		{InstallMethodDocker, "docker"},
		{InstallMethodGoInstall, "go install"},
		{InstallMethod(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.method.String()
			if got != tt.want {
				t.Errorf("InstallMethod(%d).String() = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 bytes"},
		{500, "500 bytes"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello world\n")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	correctHash := "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	t.Run("matching hash", func(t *testing.T) {
		if err := VerifyChecksum(tmpFile, correctHash); err != nil {
			t.Errorf("VerifyChecksum() unexpected error for correct hash: %v", err)
		}
	})

	t.Run("mismatching hash", func(t *testing.T) {
		err := VerifyChecksum(tmpFile, wrongHash)
		if err == nil {
			t.Error("VerifyChecksum() expected error for wrong hash")
		}
		if !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("VerifyChecksum() error should mention mismatch: %v", err)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		if err := VerifyChecksum("/nonexistent/file", correctHash); err == nil {
			t.Error("VerifyChecksum() expected error for nonexistent file")
		}
	})
}

func TestIsCacheValid(t *testing.T) {
	tests := []struct {
		name  string
		cache *UpgradeCheckCache
		want  bool
	}{
		{name: "nil cache", cache: nil, want: false},
		{name: "fresh cache", cache: &UpgradeCheckCache{LastCheck: time.Now()}, want: true},
		{name: "expired cache", cache: &UpgradeCheckCache{LastCheck: time.Now().Add(-25 * time.Hour)}, want: false},
		{name: "just within TTL", cache: &UpgradeCheckCache{LastCheck: time.Now().Add(-23 * time.Hour)}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCacheValid(tt.cache)
			if got != tt.want {
				t.Errorf("IsCacheValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCachedUpdateInfo(t *testing.T) {
	t.Run("no cache exists", func(t *testing.T) {
		store := &mockCacheStore{}
		if result := GetCachedUpdateInfo(store); result != nil {
			t.Error("GetCachedUpdateInfo() should return nil when no cache exists")
		}
	})

	t.Run("fresh cache exists", func(t *testing.T) {
		cache := &UpgradeCheckCache{
			LastCheck:       time.Now(),
			LatestVersion:   "v1.30.3",
			CurrentVersion:  "v1.30.0",
			UpdateAvailable: true,
		}
		store := &mockCacheStore{cache: cache}

		result := GetCachedUpdateInfo(store)
		require.NotNil(t, result, "GetCachedUpdateInfo() should return cache")
		if result.LatestVersion != cache.LatestVersion {
			t.Errorf("GetCachedUpdateInfo().LatestVersion = %q, want %q", result.LatestVersion, cache.LatestVersion)
		}
	})

	t.Run("very stale cache", func(t *testing.T) {
		cache := &UpgradeCheckCache{
			LastCheck:       time.Now().Add(-50 * time.Hour),
			LatestVersion:   "v1.30.3",
			CurrentVersion:  "v1.30.0",
			UpdateAvailable: true,
		}
		store := &mockCacheStore{cache: cache}

		if result := GetCachedUpdateInfo(store); result != nil {
			t.Error("GetCachedUpdateInfo() should return nil for very stale cache")
		}
	})
}

func TestCheckAndUpdateCacheDevVersion(t *testing.T) {
	store := &mockCacheStore{}
	devVersions := []string{"dev", "0.0.0"}
	for _, v := range devVersions {
		cache, err := CheckAndUpdateCache(context.Background(), store, v)
		if err != nil {
			t.Errorf("CheckAndUpdateCache(%s) error: %v", v, err)
		}
		if cache != nil {
			t.Errorf("CheckAndUpdateCache(%s) should return nil", v)
		}
	}
}

func TestCheckAndUpdateCacheWithValidCache(t *testing.T) {
	cache := &UpgradeCheckCache{
		LastCheck:       time.Now(),
		LatestVersion:   "v1.30.3",
		CurrentVersion:  "v1.30.0",
		UpdateAvailable: true,
	}
	store := &mockCacheStore{cache: cache}

	result, err := CheckAndUpdateCache(context.Background(), store, "v1.30.0")
	if err != nil {
		t.Fatalf("CheckAndUpdateCache() error: %v", err)
	}
	require.NotNil(t, result, "CheckAndUpdateCache() should return cache")
	if result.LatestVersion != cache.LatestVersion {
		t.Errorf("CheckAndUpdateCache().LatestVersion = %q, want %q", result.LatestVersion, cache.LatestVersion)
	}
}

func TestProgressWriter(t *testing.T) {
	var buf strings.Builder
	var lastDownloaded, lastTotal int64

	pw := &progressWriter{
		writer: &buf,
		total:  100,
		onProgress: func(downloaded, total int64) {
			lastDownloaded = downloaded
			lastTotal = total
		},
	}

	n, err := pw.Write([]byte("hello"))
	if err != nil {
		t.Errorf("Write() error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write() = %d, want 5", n)
	}
	if pw.written != 5 {
		t.Errorf("written = %d, want 5", pw.written)
	}
	if lastDownloaded != 5 {
		t.Errorf("lastDownloaded = %d, want 5", lastDownloaded)
	}
	if lastTotal != 100 {
		t.Errorf("lastTotal = %d, want 100", lastTotal)
	}

	n, err = pw.Write([]byte(" world"))
	if err != nil {
		t.Errorf("Write() error: %v", err)
	}
	if n != 6 {
		t.Errorf("Write() = %d, want 6", n)
	}
	if pw.written != 11 {
		t.Errorf("written = %d, want 11", pw.written)
	}
	if buf.String() != "hello world" {
		t.Errorf("buffer = %q, want %q", buf.String(), "hello world")
	}
}

func TestProgressWriterNilCallback(t *testing.T) {
	var buf strings.Builder
	pw := &progressWriter{
		writer:     &buf,
		total:      100,
		onProgress: nil,
	}

	n, err := pw.Write([]byte("test"))
	if err != nil {
		t.Errorf("Write() error: %v", err)
	}
	if n != 4 {
		t.Errorf("Write() = %d, want 4", n)
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")
	content := []byte("test content for copy")

	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read destination: %v", err)
	}
	if string(dstContent) != string(content) {
		t.Errorf("copyFile() content = %q, want %q", dstContent, content)
	}
}

func TestCopyFileErrors(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("source not found", func(t *testing.T) {
		if err := copyFile("/nonexistent/file", filepath.Join(tmpDir, "dest.txt")); err == nil {
			t.Error("copyFile() should error for non-existent source")
		}
	})

	t.Run("invalid destination", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "source.txt")
		if err := os.WriteFile(srcPath, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create source: %v", err)
		}
		if err := copyFile(srcPath, "/nonexistent/dir/file.txt"); err == nil {
			t.Error("copyFile() should error for invalid destination path")
		}
	})
}

func TestCheckWritePermission(t *testing.T) {
	t.Run("writable directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		targetPath := filepath.Join(tmpDir, "test-binary")
		if err := CheckWritePermission(targetPath); err != nil {
			t.Errorf("CheckWritePermission() error for writable dir: %v", err)
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		if err := CheckWritePermission("/nonexistent/path/binary"); err == nil {
			t.Error("CheckWritePermission() should error for non-existent directory")
		}
	})
}

func TestFindBinary(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("binary at root", func(t *testing.T) {
		binaryPath := filepath.Join(tmpDir, "dagu")
		if err := os.WriteFile(binaryPath, []byte("binary"), 0755); err != nil {
			t.Fatalf("Failed to create test binary: %v", err)
		}

		found, err := findBinary(tmpDir, "dagu")
		if err != nil {
			t.Fatalf("findBinary() error: %v", err)
		}
		if found != binaryPath {
			t.Errorf("findBinary() = %q, want %q", found, binaryPath)
		}
	})

	t.Run("binary in subdirectory", func(t *testing.T) {
		subDir := filepath.Join(tmpDir, "subdir")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatalf("Failed to create subdir: %v", err)
		}
		binaryPath := filepath.Join(subDir, "other-binary")
		if err := os.WriteFile(binaryPath, []byte("binary"), 0755); err != nil {
			t.Fatalf("Failed to create test binary: %v", err)
		}

		found, err := findBinary(tmpDir, "other-binary")
		if err != nil {
			t.Fatalf("findBinary() error: %v", err)
		}
		if found != binaryPath {
			t.Errorf("findBinary() = %q, want %q", found, binaryPath)
		}
	})

	t.Run("binary not found", func(t *testing.T) {
		emptyDir := filepath.Join(tmpDir, "empty")
		if err := os.MkdirAll(emptyDir, 0755); err != nil {
			t.Fatalf("Failed to create empty dir: %v", err)
		}
		if _, err := findBinary(emptyDir, "nonexistent"); err == nil {
			t.Error("findBinary() should error when binary not found")
		}
	})
}

func TestExtractArchive(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}

	createTestTarGz(t, archivePath, map[string]string{
		"dagu":       "#!/bin/sh\necho test",
		"readme.txt": "test readme",
	})

	ctx := context.Background()
	if err := extractArchive(ctx, archivePath, extractDir); err != nil {
		t.Fatalf("extractArchive() error: %v", err)
	}

	daguPath := filepath.Join(extractDir, "dagu")
	if _, err := os.Stat(daguPath); os.IsNotExist(err) {
		t.Error("extractArchive() did not extract dagu binary")
	}

	content, err := os.ReadFile(daguPath)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}
	if string(content) != "#!/bin/sh\necho test" {
		t.Errorf("extractArchive() content mismatch")
	}
}

func TestExtractArchiveInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}

	ctx := context.Background()
	if err := extractArchive(ctx, "/nonexistent/archive.tar.gz", extractDir); err == nil {
		t.Error("extractArchive() should error for non-existent archive")
	}
}

func TestExtractArchiveWithSubdirectory(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Failed to create archive: %v", err)
	}

	gzWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzWriter)

	dirHeader := &tar.Header{
		Name:     "subdir/",
		Mode:     0755,
		Typeflag: tar.TypeDir,
	}
	if err := tarWriter.WriteHeader(dirHeader); err != nil {
		t.Fatalf("Failed to write dir header: %v", err)
	}

	content := "file in subdir"
	fileHeader := &tar.Header{
		Name: "subdir/file.txt",
		Mode: 0644,
		Size: int64(len(content)),
	}
	if err := tarWriter.WriteHeader(fileHeader); err != nil {
		t.Fatalf("Failed to write file header: %v", err)
	}
	if _, err := io.WriteString(tarWriter, content); err != nil {
		t.Fatalf("Failed to write file content: %v", err)
	}

	_ = tarWriter.Close()
	_ = gzWriter.Close()
	_ = file.Close()

	ctx := context.Background()
	if err := extractArchive(ctx, archivePath, extractDir); err != nil {
		t.Fatalf("extractArchive() error: %v", err)
	}

	extractedPath := filepath.Join(extractDir, "subdir", "file.txt")
	extractedContent, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}
	if string(extractedContent) != content {
		t.Errorf("extractArchive() content = %q, want %q", extractedContent, content)
	}
}

func TestInstall(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "dagu_1.30.3_darwin_arm64.tar.gz")
	binaryName := testBinaryName("dagu")
	targetPath := filepath.Join(tmpDir, "target", binaryName)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatalf("Failed to create target dir: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("old binary"), 0755); err != nil {
		t.Fatalf("Failed to create old binary: %v", err)
	}

	createTestTarGz(t, archivePath, map[string]string{
		binaryName: "#!/bin/sh\necho new",
	})

	ctx := context.Background()
	result, err := Install(ctx, InstallOptions{
		ArchivePath:  archivePath,
		TargetPath:   targetPath,
		CreateBackup: true,
	})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	if !result.Installed {
		t.Error("Install() Installed should be true")
	}
	if result.BackupPath == "" {
		t.Error("Install() BackupPath should not be empty when CreateBackup is true")
	}
	if _, err := os.Stat(result.BackupPath); os.IsNotExist(err) {
		t.Error("Install() did not create backup")
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read installed binary: %v", err)
	}
	if string(content) != "#!/bin/sh\necho new" {
		t.Errorf("Install() binary content mismatch, got %q", string(content))
	}
}

func TestInstallWithoutBackup(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	binaryName := testBinaryName("dagu")
	targetPath := filepath.Join(tmpDir, binaryName)

	createTestTarGz(t, archivePath, map[string]string{
		binaryName: "#!/bin/sh\necho test",
	})

	ctx := context.Background()
	result, err := Install(ctx, InstallOptions{
		ArchivePath:  archivePath,
		TargetPath:   targetPath,
		CreateBackup: false,
	})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	if !result.Installed {
		t.Error("Install() Installed should be true")
	}
	if result.BackupPath != "" {
		t.Error("Install() BackupPath should be empty when CreateBackup is false")
	}
}

func TestInstallErrors(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("invalid archive", func(t *testing.T) {
		archivePath := filepath.Join(tmpDir, "invalid.tar.gz")
		if err := os.WriteFile(archivePath, []byte("not a valid archive"), 0644); err != nil {
			t.Fatalf("Failed to create invalid archive: %v", err)
		}

		ctx := context.Background()
		_, err := Install(ctx, InstallOptions{
			ArchivePath: archivePath,
			TargetPath:  filepath.Join(tmpDir, "dagu"),
		})
		if err == nil {
			t.Error("Install() should error for invalid archive")
		}
	})

	t.Run("missing binary in archive", func(t *testing.T) {
		archivePath := filepath.Join(tmpDir, "nobinary.tar.gz")
		createTestTarGz(t, archivePath, map[string]string{
			"readme.txt": "no binary here",
		})

		ctx := context.Background()
		_, err := Install(ctx, InstallOptions{
			ArchivePath: archivePath,
			TargetPath:  filepath.Join(tmpDir, "dagu2"),
		})
		if err == nil {
			t.Error("Install() should error when binary not found in archive")
		}
	})
}

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.30.3", "v1.30.3"},
		{"v1.30.3", "v1.30.3"},
		{"2.0.0", "v2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := formatVersion(tt.input)
			if got != tt.want {
				t.Errorf("formatVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatResult(t *testing.T) {
	t.Run("dry run mode", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.0",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  true,
			DryRun:         true,
			AssetName:      "dagu_1.30.3_darwin_arm64.tar.gz",
			AssetSize:      1024 * 1024,
			ExecutablePath: "/usr/local/bin/dagu",
		}

		output := FormatResult(result)
		if !strings.Contains(output, "Dry run") {
			t.Error("FormatResult() should contain 'Dry run' text")
		}
		if !strings.Contains(output, "v1.30.0") {
			t.Error("FormatResult() should contain current version")
		}
		if !strings.Contains(output, "v1.30.3") {
			t.Error("FormatResult() should contain target version")
		}
		if strings.Contains(output, "Target version:  ") {
			t.Error("FormatResult() should not add extra spacing before target version")
		}
	})

	t.Run("already latest", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.3",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  false,
			WasUpgraded:    false,
		}

		output := FormatResult(result)
		if !strings.Contains(output, "latest version") {
			t.Error("FormatResult() should indicate already on latest version")
		}
	})

	t.Run("successful upgrade with backup", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.0",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  true,
			WasUpgraded:    true,
			BackupPath:     "/usr/local/bin/dagu.bak",
		}

		output := FormatResult(result)
		if !strings.Contains(output, "successful") {
			t.Error("FormatResult() should indicate successful upgrade")
		}
		if !strings.Contains(output, "Backup") {
			t.Error("FormatResult() should mention backup")
		}
	})

	t.Run("successful upgrade without backup", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.0",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  true,
			WasUpgraded:    true,
			BackupPath:     "",
		}

		output := FormatResult(result)
		if !strings.Contains(output, "successful") {
			t.Error("FormatResult() should indicate successful upgrade")
		}
	})
}

func TestFormatCheckResult(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.0",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  true,
		}

		output := FormatCheckResult(result)
		if !strings.Contains(output, "v1.30.0") {
			t.Error("FormatCheckResult() should contain current version")
		}
		if !strings.Contains(output, "v1.30.3") {
			t.Error("FormatCheckResult() should contain latest version")
		}
		if strings.Contains(output, "Latest version:  ") {
			t.Error("FormatCheckResult() should not add extra spacing before latest version")
		}
		if !strings.Contains(output, "update is available") {
			t.Error("FormatCheckResult() should indicate update available")
		}
	})

	t.Run("up to date", func(t *testing.T) {
		result := &Result{
			CurrentVersion: "1.30.3",
			TargetVersion:  "v1.30.3",
			UpgradeNeeded:  false,
		}

		output := FormatCheckResult(result)
		if !strings.Contains(output, "Latest version") {
			t.Error("FormatCheckResult() should show 'Latest version' label")
		}
	})

	t.Run("specific version shows target label", func(t *testing.T) {
		result := &Result{
			CurrentVersion:         "1.30.0",
			TargetVersion:          "v1.30.3",
			UpgradeNeeded:          true,
			SpecificVersionRequest: true,
		}

		output := FormatCheckResult(result)
		if !strings.Contains(output, "Target version") {
			t.Error("FormatCheckResult() should show 'Target version' when specific version requested")
		}
		if strings.Contains(output, "Target version:  ") {
			t.Error("FormatCheckResult() should not add extra spacing before target version")
		}
		if strings.Contains(output, "Latest version") {
			t.Error("FormatCheckResult() should not show 'Latest version' when specific version requested")
		}
	})

	t.Run("latest version shows latest label", func(t *testing.T) {
		result := &Result{
			CurrentVersion:         "1.30.0",
			TargetVersion:          "v1.30.3",
			UpgradeNeeded:          true,
			SpecificVersionRequest: false,
		}

		output := FormatCheckResult(result)
		if !strings.Contains(output, "Latest version") {
			t.Error("FormatCheckResult() should show 'Latest version' when no specific version requested")
		}
	})
}

func TestCanSelfUpgrade(t *testing.T) {
	canUpgrade, reason := CanSelfUpgrade()

	if canUpgrade && reason != "" {
		t.Error("CanSelfUpgrade() should return empty reason when upgrade is possible")
	}
	if !canUpgrade && reason == "" {
		t.Error("CanSelfUpgrade() should return reason when upgrade is not possible")
	}
}

func TestNewGitHubClient(t *testing.T) {
	client := NewGitHubClient()
	require.NotNil(t, client, "NewGitHubClient() returned nil")
	if client.client == nil {
		t.Error("NewGitHubClient() returned client with nil http client")
	}
}

func TestGetLatestRelease(t *testing.T) {
	release := Release{
		TagName:    "v1.30.3",
		Name:       "Release v1.30.3",
		Draft:      false,
		Prerelease: false,
		HTMLURL:    "https://github.com/dagucloud/dagu/releases/tag/v1.30.3",
		Assets: []Asset{
			{Name: "dagu_1.30.3_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/asset"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(release)
		case "/releases":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]Release{release})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestGitHubClient(server.URL)

	t.Run("without prerelease", func(t *testing.T) {
		ctx := context.Background()
		result, err := client.GetLatestRelease(ctx, false)
		if err != nil {
			t.Fatalf("GetLatestRelease() error: %v", err)
		}
		if result.TagName != "v1.30.3" {
			t.Errorf("GetLatestRelease().TagName = %q, want %q", result.TagName, "v1.30.3")
		}
	})

	t.Run("with prerelease", func(t *testing.T) {
		ctx := context.Background()
		result, err := client.GetLatestRelease(ctx, true)
		if err != nil {
			t.Fatalf("GetLatestRelease() error: %v", err)
		}
		if result.TagName != "v1.30.3" {
			t.Errorf("GetLatestRelease().TagName = %q, want %q", result.TagName, "v1.30.3")
		}
	})
}

func TestGetChecksums(t *testing.T) {
	checksumContent := `abc123def456  dagu_1.30.3_darwin_arm64.tar.gz
789xyz012345  dagu_1.30.3_linux_amd64.tar.gz`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums.txt" {
			_, _ = w.Write([]byte(checksumContent))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	release := &Release{
		TagName: "v1.30.3",
		Assets: []Asset{
			{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums.txt"},
		},
	}

	client := NewGitHubClient()
	ctx := context.Background()
	checksums, err := client.GetChecksums(ctx, release)
	if err != nil {
		t.Fatalf("GetChecksums() error: %v", err)
	}

	if len(checksums) != 2 {
		t.Errorf("GetChecksums() returned %d checksums, want 2", len(checksums))
	}
	if checksums["dagu_1.30.3_darwin_arm64.tar.gz"] != "abc123def456" {
		t.Error("GetChecksums() wrong checksum for darwin")
	}
}

func TestGetChecksumsNoChecksumsFile(t *testing.T) {
	release := &Release{
		TagName: "v1.30.3",
		Assets: []Asset{
			{Name: "dagu_1.30.3_darwin_arm64.tar.gz"},
		},
	}

	client := NewGitHubClient()
	ctx := context.Background()
	if _, err := client.GetChecksums(ctx, release); err == nil {
		t.Error("GetChecksums() should error when checksums.txt not found")
	}
}

func TestDownload(t *testing.T) {
	content := []byte("test binary content")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded-file")

	var progressCalls int
	ctx := context.Background()
	err := Download(ctx, DownloadOptions{
		URL:          server.URL + "/test-file",
		Destination:  destPath,
		ExpectedHash: expectedHash,
		OnProgress: func(_, _ int64) {
			progressCalls++
		},
	})
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}

	downloaded, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}
	if string(downloaded) != string(content) {
		t.Error("Download() content mismatch")
	}
	if progressCalls == 0 {
		t.Error("Download() progress callback not called")
	}
}

func TestDownloadErrors(t *testing.T) {
	t.Run("bad checksum", func(t *testing.T) {
		content := []byte("test binary content")
		wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(content)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		destPath := filepath.Join(tmpDir, "downloaded-file")

		ctx := context.Background()
		err := Download(ctx, DownloadOptions{
			URL:          server.URL + "/test-file",
			Destination:  destPath,
			ExpectedHash: wrongHash,
		})
		if err == nil {
			t.Error("Download() should error on checksum mismatch")
		}
		if !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("Download() error should mention checksum mismatch: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		destPath := filepath.Join(tmpDir, "downloaded-file")

		ctx := context.Background()
		err := Download(ctx, DownloadOptions{
			URL:         server.URL + "/not-found",
			Destination: destPath,
		})
		if err == nil {
			t.Error("Download() should error on 404")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		blocked := make(chan struct{})
		defer close(blocked)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-blocked
			_, _ = w.Write([]byte("content"))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		destPath := filepath.Join(tmpDir, "file")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := Download(ctx, DownloadOptions{
			URL:         server.URL + "/file",
			Destination: destPath,
		})
		if err == nil {
			t.Error("Download() should error on cancelled context")
		}
	})
}

func TestDownloadWithoutChecksum(t *testing.T) {
	content := []byte("test binary content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded-file")

	ctx := context.Background()
	err := Download(ctx, DownloadOptions{
		URL:          server.URL + "/test-file",
		Destination:  destPath,
		ExpectedHash: "",
	})
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}

	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Error("Download() did not create file")
	}
}

func TestVerifyBinary(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("successful verification", func(t *testing.T) {
		scriptPath := createVersionTestBinary(t, tmpDir, "dagu-test", "1.30.3")

		if err := VerifyBinary(scriptPath, "v1.30.3"); err != nil {
			t.Errorf("VerifyBinary() error: %v", err)
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		scriptPath := createVersionTestBinary(t, tmpDir, "dagu-wrong", "1.29.0")

		if err := VerifyBinary(scriptPath, "v1.30.3"); err == nil {
			t.Error("VerifyBinary() should error on version mismatch")
		}
	})

	t.Run("binary execution fails", func(t *testing.T) {
		if err := VerifyBinary("/nonexistent/binary", "v1.30.3"); err == nil {
			t.Error("VerifyBinary() should error for non-existent binary")
		}
	})
}

func TestGetExecutablePath(t *testing.T) {
	path, err := GetExecutablePath()
	if err != nil {
		t.Fatalf("GetExecutablePath() error: %v", err)
	}
	if path == "" {
		t.Error("GetExecutablePath() returned empty path")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("GetExecutablePath() returned non-existent path")
	}
}

func TestReplaceUnixBinary(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source")
	targetPath := filepath.Join(tmpDir, "target")

	if err := os.WriteFile(srcPath, []byte("new binary"), 0755); err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("old binary"), 0644); err != nil {
		t.Fatalf("Failed to create target: %v", err)
	}

	if err := replaceUnixBinary(srcPath, targetPath, 0755); err != nil {
		t.Fatalf("replaceUnixBinary() error: %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target: %v", err)
	}
	if string(content) != "new binary" {
		t.Error("replaceUnixBinary() did not replace content")
	}

	assertPermsIfSupported(t, targetPath, 0755)
}

func TestReplaceBinary(t *testing.T) {
	t.Run("existing target", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcPath := filepath.Join(tmpDir, "source")
		targetPath := filepath.Join(tmpDir, "target")

		if err := os.WriteFile(srcPath, []byte("new binary"), 0755); err != nil {
			t.Fatalf("Failed to create source: %v", err)
		}
		if err := os.WriteFile(targetPath, []byte("old binary"), 0700); err != nil {
			t.Fatalf("Failed to create target: %v", err)
		}

		if err := replaceBinary(srcPath, targetPath); err != nil {
			t.Fatalf("replaceBinary() error: %v", err)
		}

		content, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("Failed to read target: %v", err)
		}
		if string(content) != "new binary" {
			t.Error("replaceBinary() did not replace content")
		}

		assertPermsIfSupported(t, targetPath, 0700)
	})

	t.Run("new target", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcPath := filepath.Join(tmpDir, "source")
		targetPath := filepath.Join(tmpDir, "newtarget")

		if err := os.WriteFile(srcPath, []byte("new binary"), 0755); err != nil {
			t.Fatalf("Failed to create source: %v", err)
		}

		if err := replaceBinary(srcPath, targetPath); err != nil {
			t.Fatalf("replaceBinary() error: %v", err)
		}

		assertPermsIfSupported(t, targetPath, 0755)
	})
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"123", true},
		{"abc", false},
		{"12a3", false},
		{"0", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isNumeric(tt.input)
			if got != tt.want {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetLatestReleaseRetryOnServerError(t *testing.T) {
	release := Release{
		TagName: "v1.30.3",
		Name:    "Release v1.30.3",
	}

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	client := newTestGitHubClient(server.URL)

	ctx := context.Background()
	result, err := client.GetLatestRelease(ctx, false)
	if err != nil {
		t.Fatalf("GetLatestRelease() error: %v", err)
	}
	if result.TagName != "v1.30.3" {
		t.Errorf("GetLatestRelease().TagName = %q, want %q", result.TagName, "v1.30.3")
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestGetRelease404NotRetried(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestGitHubClient(server.URL)

	ctx := context.Background()
	_, err := client.GetRelease(ctx, "v99.99.99")
	if err == nil {
		t.Fatal("GetRelease() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("GetRelease() error should contain 'not found': %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for 404), got %d", attempts)
	}
}

// newTestGitHubClient creates a GitHubClient that redirects all requests
// from the real GitHub API URL to the given test server URL.
func newTestGitHubClient(serverURL string) *GitHubClient {
	client := NewGitHubClient()
	client.client.OnBeforeRequest(func(_ *resty.Client, req *resty.Request) error {
		req.URL = strings.Replace(req.URL, githubAPIURL, serverURL+"/releases", 1)
		return nil
	})
	return client
}

func TestDownloadRetryOnServerError(t *testing.T) {
	content := []byte("test binary content")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])

	var getAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			return
		}
		getAttempts++
		if getAttempts <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded-file")

	ctx := context.Background()
	err := Download(ctx, DownloadOptions{
		URL:          server.URL + "/test-file",
		Destination:  destPath,
		ExpectedHash: expectedHash,
	})
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}

	downloaded, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}
	if string(downloaded) != string(content) {
		t.Error("Download() content mismatch")
	}
	if getAttempts != 3 {
		t.Errorf("expected 3 GET attempts, got %d", getAttempts)
	}
}

func TestDownloadPermanent404(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded-file")

	ctx := context.Background()
	err := Download(ctx, DownloadOptions{
		URL:         server.URL + "/not-found",
		Destination: destPath,
	})
	if err == nil {
		t.Fatal("Download() expected error, got nil")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for 404), got %d", attempts)
	}
}

func TestValidateVersionTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{name: "valid tag", tag: "v1.30.3", wantErr: false},
		{name: "valid prerelease", tag: "v1.30.0-rc.1", wantErr: false},
		{name: "forward slash", tag: "v1.30.3/../../etc/passwd", wantErr: true},
		{name: "backslash", tag: `v1.30.3\..\secret`, wantErr: true},
		{name: "double dot traversal", tag: "v1.30.3..secret", wantErr: true},
		{name: "control character", tag: "v1.30.3\x00", wantErr: true},
		{name: "newline", tag: "v1.30.3\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVersionTag(tt.tag)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateVersionTag(%q) expected error, got nil", tt.tag)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateVersionTag(%q) unexpected error: %v", tt.tag, err)
			}
		})
	}
}

func TestInstallBackupTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "dagu_1.30.3_darwin_arm64.tar.gz")
	binaryName := testBinaryName("dagu")
	targetPath := filepath.Join(tmpDir, "target", binaryName)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatalf("Failed to create target dir: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("old binary"), 0755); err != nil {
		t.Fatalf("Failed to create old binary: %v", err)
	}

	createTestTarGz(t, archivePath, map[string]string{
		binaryName: "#!/bin/sh\necho new",
	})

	// Create an existing .bak file
	existingBak := targetPath + ".bak"
	if err := os.WriteFile(existingBak, []byte("previous backup"), 0644); err != nil {
		t.Fatalf("Failed to create existing backup: %v", err)
	}

	ctx := context.Background()
	result, err := Install(ctx, InstallOptions{
		ArchivePath:  archivePath,
		TargetPath:   targetPath,
		CreateBackup: true,
	})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// The backup path should not be the plain .bak (it should be timestamped)
	if result.BackupPath == existingBak {
		t.Error("Install() should use timestamped backup name when .bak already exists")
	}
	if result.BackupPath == "" {
		t.Fatal("Install() BackupPath should not be empty")
	}

	// Verify the original .bak is preserved
	origContent, err := os.ReadFile(existingBak)
	if err != nil {
		t.Fatalf("Original .bak should still exist: %v", err)
	}
	if string(origContent) != "previous backup" {
		t.Error("Original .bak content should be preserved")
	}

	// Verify the timestamped backup exists
	if _, err := os.Stat(result.BackupPath); os.IsNotExist(err) {
		t.Error("Timestamped backup should exist")
	}
}

func TestReplaceWindowsBinary(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source")
	targetPath := filepath.Join(tmpDir, "target")

	if err := os.WriteFile(srcPath, []byte("new binary"), 0755); err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("old binary"), 0644); err != nil {
		t.Fatalf("Failed to create target: %v", err)
	}

	if err := replaceWindowsBinary(srcPath, targetPath, 0755); err != nil {
		t.Fatalf("replaceWindowsBinary() error: %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target: %v", err)
	}
	if string(content) != "new binary" {
		t.Error("replaceWindowsBinary() did not replace content")
	}

	assertPermsIfSupported(t, targetPath, 0755)

	// .old should have been cleaned up
	if _, err := os.Stat(targetPath + ".old"); !os.IsNotExist(err) {
		t.Error("replaceWindowsBinary() should clean up .old file")
	}
}

// createTestTarGz creates a test tar.gz archive with the specified files.
func createTestTarGz(t testing.TB, path string, files map[string]string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create archive file: %v", err)
	}
	defer func() { _ = file.Close() }()

	gzWriter := gzip.NewWriter(file)
	defer func() { _ = gzWriter.Close() }()

	tarWriter := tar.NewWriter(gzWriter)
	defer func() { _ = tarWriter.Close() }()

	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0755,
			Size: int64(len(content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("Failed to write tar header: %v", err)
		}
		if _, err := io.WriteString(tarWriter, content); err != nil {
			t.Fatalf("Failed to write tar content: %v", err)
		}
	}
}
