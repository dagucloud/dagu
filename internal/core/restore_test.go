// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestoreUnpersistedFrom(t *testing.T) {
	t.Parallel()

	// Stands in for a DAG decoded from dag.json: the persisted fields survived,
	// every omitted one is missing, and the build outcome is its own.
	dst := &core.DAG{
		Name:          "persisted-name",
		Queue:         "persisted-queue",
		EnvEvaluated:  true,
		BuildErrors:   []error{errors.New("outcome of restoring")},
		BuildWarnings: []string{"warning from restoring"},
	}

	// Stands in for the same DAG rebuilt from its source.
	src := &core.DAG{
		Name:               "rebuilt-name",
		Queue:              "rebuilt-queue",
		Params:             []string{"topic=rebuilt"},
		ParamsJSON:         `{"topic":"rebuilt"}`,
		SSH:                &core.SSHConfig{Host: "ssh.example.com"},
		S3:                 &core.S3Config{Region: "us-west-2"},
		Redis:              &core.RedisConfig{Host: "redis.example.com"},
		SMTP:               &core.SMTPConfig{Host: "smtp.example.com"},
		Kubernetes:         core.KubernetesConfig{"namespace": "dag-ns"},
		RegistryAuths:      map[string]*core.AuthConfig{"registry.example.com": {Username: "user"}},
		WorkingDirExplicit: true,
		EnvEvaluated:       false,
		BuildErrors:        []error{errors.New("outcome of rebuilding")},
		BuildWarnings:      []string{"warning from rebuilding"},
	}

	dst.RestoreUnpersistedFrom(src)

	// Fields JSON omits come across, because only the rebuild has them.
	assert.Equal(t, []string{"topic=rebuilt"}, dst.Params)
	assert.JSONEq(t, `{"topic":"rebuilt"}`, dst.ParamsJSON)
	require.NotNil(t, dst.SSH)
	assert.Equal(t, "ssh.example.com", dst.SSH.Host)
	require.NotNil(t, dst.S3)
	assert.Equal(t, "us-west-2", dst.S3.Region)
	require.NotNil(t, dst.Redis)
	assert.Equal(t, "redis.example.com", dst.Redis.Host)
	require.NotNil(t, dst.SMTP)
	assert.Equal(t, "smtp.example.com", dst.SMTP.Host)
	assert.Equal(t, "dag-ns", dst.Kubernetes["namespace"])
	require.Contains(t, dst.RegistryAuths, "registry.example.com")
	assert.Equal(t, "user", dst.RegistryAuths["registry.example.com"].Username)
	assert.True(t, dst.WorkingDirExplicit)

	// Fields JSON keeps are the restored DAG's own and must survive intact.
	assert.Equal(t, "persisted-name", dst.Name)
	assert.Equal(t, "persisted-queue", dst.Queue)

	// The rebuild's outcome describes the rebuild, not the run being restored.
	assert.True(t, dst.EnvEvaluated)
	require.Len(t, dst.BuildErrors, 1)
	assert.EqualError(t, dst.BuildErrors[0], "outcome of restoring")
	assert.Equal(t, []string{"warning from restoring"}, dst.BuildWarnings)
}
