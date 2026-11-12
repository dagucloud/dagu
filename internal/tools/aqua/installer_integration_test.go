// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/tools"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

func TestInstallerInstallIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	toolsDir := filepath.Join(t.TempDir(), "tools")
	workDir := t.TempDir()
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Provider: "aqua",
		Packages: []ir.ToolPackage{{
			Name:    "jq",
			Package: "jqlang/jq",
			Version: "jq-1.7.1",
		}},
	}, tools.InstallOptions{
		ToolsDir: toolsDir,
		WorkDir:  workDir,
	})

	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "aqua.yaml"))
	require.FileExists(t, manifest.Checksum)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "manifest.json"))
	require.NotEmpty(t, manifest.Commands["jq"].Path)
	require.Equal(t, filepath.Join(toolsDir, "aqua", "root"), manifest.RootDir)
	require.Equal(t, filepath.Join(manifest.EnvDir, "bin"), manifest.BinDir)
	require.Equal(t, filepath.Join(manifest.BinDir, filepath.Base(manifest.Commands["jq"].Path)), manifest.Commands["jq"].Path)
	require.FileExists(t, manifest.Commands["jq"].Path)
}

func TestInstallerDigestPinningIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	jq := ir.ToolPackage{Name: "jq", Package: "jqlang/jq", Version: "jq-1.7.1"}

	// First install records the artifact checksum aqua verified.
	seedDir := filepath.Join(t.TempDir(), "tools")
	seedManifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{jq},
	}, tools.InstallOptions{ToolsDir: seedDir, WorkDir: t.TempDir()})
	require.NoError(t, err)

	data, err := os.ReadFile(seedManifest.Checksum)
	require.NoError(t, err)
	var recorded checksumFileContent
	require.NoError(t, json.Unmarshal(data, &recorded))
	digest := ""
	for _, entry := range recorded.Checksums {
		if strings.Contains(entry.ID, "github.com/jqlang/jq/jq-1.7.1/") {
			digest = "sha256:" + strings.ToLower(entry.Checksum)
			break
		}
	}
	require.NotEmpty(t, digest, "expected a recorded jq artifact checksum")

	// A fresh tools dir with the recorded digest must verify and expose an
	// aqua root isolated under the toolset env.
	pinned := jq
	pinned.Digest = digest
	goodDir := filepath.Join(t.TempDir(), "tools")
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{pinned},
	}, tools.InstallOptions{ToolsDir: goodDir, WorkDir: t.TempDir()})
	require.NoError(t, err)
	require.FileExists(t, manifest.Commands["jq"].Path)
	require.Equal(t, filepath.Join(manifest.EnvDir, "root"), manifest.RootDir)

	// A wrong digest must fail with a mismatch naming both hashes.
	wrong := jq
	wrong.Digest = "sha256:" + strings.Repeat("0", 64)
	_, err = New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{wrong},
	}, tools.InstallOptions{ToolsDir: filepath.Join(t.TempDir(), "tools"), WorkDir: t.TempDir()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "digest mismatch for jqlang/jq@jq-1.7.1")
}

func TestInstallerLatestRegistryIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	// earendil-works/pi entered the aqua registry on 2026-05-11, after the
	// compiled-in bootstrap ref; installing it proves resolution runs against
	// the latest registry release rather than the bootstrap snapshot.
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Provider: "aqua",
		Packages: []ir.ToolPackage{{
			Package: "earendil-works/pi",
			Version: "v0.83.0",
		}},
	}, tools.InstallOptions{
		ToolsDir: filepath.Join(t.TempDir(), "tools"),
		WorkDir:  t.TempDir(),
	})

	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.NotEmpty(t, manifest.Commands["pi"].Path)
	require.FileExists(t, manifest.Commands["pi"].Path)
}

// A registry edit must install new bytes even when the package version and URL stay the same.
func TestInstallerLocalRegistryIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua integration tests")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("package main\nimport \"fmt\"\nvar reply string\nfunc main(){fmt.Print(reply)}\n"), 0o600))
	command := "tool"
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	artifacts := make(map[string][]byte)
	for _, reply := range []string{"one", "two"} {
		binary := filepath.Join(dir, command)
		output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "-ldflags=-X main.reply="+reply, source).CombinedOutput()
		require.NoError(t, err, "%s", output)
		artifacts[reply], err = os.ReadFile(binary)
		require.NoError(t, err)
	}
	var artifact atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+command {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(artifact.Load().([]byte))
	}))
	defer server.Close()
	path := filepath.Join(dir, "registry.yaml")
	cfg := &ir.ToolConfig{Registry: &ir.ToolRegistry{Type: "local", Path: path},
		Packages: []ir.ToolPackage{{Package: "example/tool", Version: "v1.0.0", Commands: []string{command}}}}
	opts := tools.InstallOptions{ToolsDir: filepath.Join(dir, "tools"), WorkDir: dir}
	var previous *tools.Manifest
	for _, reply := range []string{"one", "two"} {
		artifact.Store(artifacts[reply])
		registry := fmt.Sprintf("# revision: %s\npackages:\n  - name: example/tool\n    type: http\n    url: http://registry.test/%s\n    format: raw\n    files:\n      - name: %s\n", reply, command, command)
		require.NoError(t, os.WriteFile(path, []byte(registry), 0o600))
		expectedHash, _, err := tools.ToolsetHash(effectiveToolConfig(cfg), runtime.GOOS+"/"+runtime.GOARCH)
		require.NoError(t, err)
		// Change the source after hashing but before the install finishes. Every
		// aqua controller must use the captured bytes, even across repeated reads.
		var edit sync.Once
		var editErr error
		client := &http.Client{Transport: localRegistryTransport{
			host: server.Listener.Addr().String(),
			onArtifact: func() error {
				edit.Do(func() { editErr = os.WriteFile(path, []byte("packages: ["), 0o600) })
				return editErr
			},
		}}
		installer := New(WithHTTPClient(client), WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))))
		manifest, err := installer.Install(t.Context(), cfg, opts)
		require.NoError(t, err)
		require.Equal(t, expectedHash, manifest.Hash)
		configBytes, err := os.ReadFile(manifest.Config)
		require.NoError(t, err)
		var aquaCfg configFile
		require.NoError(t, yaml.Unmarshal(configBytes, &aquaCfg))
		require.Len(t, aquaCfg.Registries, 1)
		snapshot := aquaCfg.Registries[0].Path
		require.NotEqual(t, path, snapshot)
		require.True(t, filepath.IsAbs(snapshot))
		snapshotBytes, err := os.ReadFile(snapshot)
		require.NoError(t, err)
		require.Equal(t, registry, string(snapshotBytes))
		sourceBytes, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "packages: [", string(sourceBytes))
		require.Equal(t, path, cfg.Registry.Path)
		require.FileExists(t, manifest.Checksum)
		output, err := exec.CommandContext(t.Context(), manifest.Commands[command].Path).CombinedOutput()
		require.NoError(t, err, "%s", output)
		require.Equal(t, reply, string(output))
		require.NoError(t, os.WriteFile(path, []byte(registry), 0o600))
		cached, err := installer.Install(t.Context(), cfg, opts)
		require.NoError(t, err)
		require.Equal(t, manifest, cached)
		if previous != nil {
			require.NotEqual(t, previous.Hash, manifest.Hash)
			require.NotEqual(t, previous.RootDir, manifest.RootDir)
		}
		previous = manifest
	}
}

// Aqua includes the URL host in cache paths; a port would make them invalid on Windows.
type localRegistryTransport struct {
	host       string
	onArtifact func() error
}

func (r localRegistryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "registry.test" {
		if r.onArtifact != nil {
			if err := r.onArtifact(); err != nil {
				return nil, err
			}
		}
		req = req.Clone(req.Context())
		req.URL.Host = r.host
	}
	return http.DefaultTransport.RoundTrip(req)
}
