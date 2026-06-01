// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package packaging_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
			if !strings.Contains(content, tiniEntrypoint) {
				t.Fatalf("%s must run /entrypoint.sh under tini", file)
			}
			if !strings.Contains(content, "tini \\") && !strings.Contains(content, "tini &&") {
				t.Fatalf("%s must install tini in the final image", file)
			}
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
			if strings.Contains(content, "\n          command:") || strings.Contains(content, "\n        command:") {
				t.Fatalf("%s must use args so the image entrypoint remains active", file)
			}
			if !strings.Contains(content, "\n          args:") && !strings.Contains(content, "\n        args:") {
				t.Fatalf("%s must pass the Dagu command through args", file)
			}
		})
	}
}

func TestDockerComposeEntrypointOverridesPreserveTini(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	content := readFile(t, filepath.Join(root, "deploy/docker/compose.minimal.yaml"))
	if strings.Contains(content, "entrypoint: []") {
		t.Fatal("compose.minimal.yaml must not clear the image entrypoint without preserving tini")
	}
	if !strings.Contains(content, `entrypoint: ["/usr/local/bin/tini", "-g", "--"]`) {
		t.Fatal("compose.minimal.yaml must keep tini as PID 1 when overriding the image entrypoint")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
