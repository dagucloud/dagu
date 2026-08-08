// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGCloneDeepCopiesKubernetesConfig(t *testing.T) {
	t.Parallel()

	original := &DAG{
		Kubernetes: KubernetesConfig{
			"namespace": "batch",
			"resources": map[string]any{
				"requests": map[string]any{
					"cpu": "100m",
				},
			},
			"volume_mounts": []any{
				map[string]any{
					"name":       "shared",
					"mount_path": "/shared",
				},
			},
		},
	}

	cloned := original.Clone()
	require.NotNil(t, cloned)
	require.NotNil(t, cloned.Kubernetes)

	cloned.Kubernetes["namespace"] = "jobs"
	resources := cloned.Kubernetes["resources"].(map[string]any)
	requests := resources["requests"].(map[string]any)
	requests["cpu"] = "250m"
	mounts := cloned.Kubernetes["volume_mounts"].([]any)
	mounts[0].(map[string]any)["mount_path"] = "/tmp/shared"

	assert.Equal(t, "batch", original.Kubernetes["namespace"])
	assert.Equal(t, "100m", original.Kubernetes["resources"].(map[string]any)["requests"].(map[string]any)["cpu"])
	assert.Equal(t, "/shared", original.Kubernetes["volume_mounts"].([]any)[0].(map[string]any)["mount_path"])
}
