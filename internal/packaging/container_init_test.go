// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package packaging_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const tiniEntrypoint = `ENTRYPOINT ["/usr/local/bin/tini", "-g", "--", "/entrypoint.sh"]`

func TestDockerfilesRunEntrypointUnderTini(t *testing.T) {
	t.Parallel()

	files := []string{
		"Dockerfile",
		"Dockerfile.alpine",
		"Dockerfile.dev",
		"deploy/docker/Dockerfile.alpine",
		"deploy/docker/Dockerfile.dev",
	}

	root := repoRoot(t)
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			content := readFile(t, filepath.Join(root, file))
			require.Contains(t, content, tiniEntrypoint, "%s must run /entrypoint.sh under tini", file)
			require.True(t,
				strings.Contains(content, "tini \\") || strings.Contains(content, "tini &&"),
				"%s must install tini in the final image", file,
			)
		})
	}
}

func TestKubernetesDaguContainersPreserveImageEntrypoint(t *testing.T) {
	t.Parallel()

	files := []string{
		"charts/dagu/templates/coordinator-deployment.yaml",
		"charts/dagu/templates/scheduler-deployment.yaml",
		"charts/dagu/templates/ui-deployment.yaml",
		"charts/dagu/templates/worker-deployment.yaml",
		"deploy/k8s/server-deployment.yaml",
		"deploy/k8s/worker-deployment.yaml",
	}

	root := repoRoot(t)
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			content := readFile(t, filepath.Join(root, file))
			require.False(t,
				strings.Contains(content, "\n          command:") || strings.Contains(content, "\n        command:"),
				"%s must use args so the image entrypoint remains active", file,
			)
			require.True(t,
				strings.Contains(content, "\n          args:") || strings.Contains(content, "\n        args:"),
				"%s must pass the Dagu command through args", file,
			)
		})
	}
}

func TestKubernetesDefaultSecurityContextKeepsKubernetes119Compatibility(t *testing.T) {
	t.Parallel()

	files := []string{
		"charts/dagu/values.yaml",
		"deploy/k8s/server-deployment.yaml",
		"deploy/k8s/worker-deployment.yaml",
	}

	root := repoRoot(t)
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			content := readFile(t, filepath.Join(root, file))
			require.Contains(t, content, "fsGroup: 1000", "%s must keep shared volumes writable after entrypoint privilege drop", file)
			require.NotContains(t,
				content,
				"\n        fsGroupChangePolicy:",
				"%s must not require a post-1.19 PodSecurityContext field by default", file,
			)
			require.NotContains(t,
				content,
				"\n  fsGroupChangePolicy:",
				"%s must not require a post-1.19 PodSecurityContext field by default", file,
			)
		})
	}
}

func TestDockerComposeEntrypointOverridesPreserveTini(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	content := readFile(t, filepath.Join(root, "deploy/docker/compose.minimal.yaml"))
	require.NotContains(t, content, "entrypoint: []", "compose.minimal.yaml must not clear the image entrypoint without preserving tini")
	require.Contains(t, content, `entrypoint: ["/usr/local/bin/tini", "-g", "--"]`, "compose.minimal.yaml must keep tini as PID 1 when overriding the image entrypoint")
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to resolve test file path")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	return string(data)
}
