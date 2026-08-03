// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testArtifactSHA = "3fa1b2c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f80"
)

func writeChecksumFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aqua-checksums.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestVerifyPackageDigestsMatch(t *testing.T) {
	t.Parallel()

	path := writeChecksumFile(t, `{"checksums":[
		{"id":"registries/github_content/github.com/aquaproj/aqua-registry/abc/registry.yaml","checksum":"FFFF","algorithm":"sha256"},
		{"id":"github_release/github.com/anomalyco/opencode/v1.18.11/opencode-darwin-arm64.zip","checksum":"`+strings.ToUpper(testArtifactSHA)+`","algorithm":"sha256"}
	]}`)

	err := verifyPackageDigests(path, []core.ToolPackage{{
		Package: "anomalyco/opencode",
		Version: "v1.18.11",
		Digest:  "sha256:" + testArtifactSHA,
	}})
	require.NoError(t, err)
}

func TestVerifyPackageDigestsMismatch(t *testing.T) {
	t.Parallel()

	path := writeChecksumFile(t, `{"checksums":[
		{"id":"github_release/github.com/anomalyco/opencode/v1.18.11/opencode-darwin-arm64.zip","checksum":"`+strings.Repeat("0", 64)+`","algorithm":"sha256"}
	]}`)

	err := verifyPackageDigests(path, []core.ToolPackage{{
		Package: "anomalyco/opencode",
		Version: "v1.18.11",
		Digest:  "sha256:" + testArtifactSHA,
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest mismatch for anomalyco/opencode@v1.18.11")
	assert.Contains(t, err.Error(), "declared sha256:"+testArtifactSHA)
}

func TestVerifyPackageDigestsNoMatchingEntry(t *testing.T) {
	t.Parallel()

	path := writeChecksumFile(t, `{"checksums":[
		{"id":"registries/github_content/github.com/aquaproj/aqua-registry/abc/registry.yaml","checksum":"FFFF","algorithm":"sha256"}
	]}`)

	err := verifyPackageDigests(path, []core.ToolPackage{{
		Package: "anomalyco/opencode",
		Version: "v1.18.11",
		Digest:  "sha256:" + testArtifactSHA,
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no recorded artifact checksum matched")
}

func TestVerifyPackageDigestsSkipsPackagesWithoutDigest(t *testing.T) {
	t.Parallel()

	err := verifyPackageDigests(filepath.Join(t.TempDir(), "missing.json"), []core.ToolPackage{{
		Package: "jqlang/jq",
		Version: "jq-1.7.1",
	}})
	require.NoError(t, err)
}

func TestVerifyPackageDigestsRejectsWrongAlgorithm(t *testing.T) {
	t.Parallel()

	path := writeChecksumFile(t, `{"checksums":[
		{"id":"github_release/github.com/anomalyco/opencode/v1.18.11/opencode-darwin-arm64.zip","checksum":"`+testArtifactSHA+`","algorithm":"sha512"}
	]}`)

	err := verifyPackageDigests(path, []core.ToolPackage{{
		Package: "anomalyco/opencode",
		Version: "v1.18.11",
		Digest:  "sha256:" + testArtifactSHA,
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `algorithm "sha512"`)
}
