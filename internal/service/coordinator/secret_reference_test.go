// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSecretReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	enc, err := crypto.NewEncryptor("test-key-for-secrets")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)

	now := time.Now().UTC()
	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/my-secret",
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "test",
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value:     "secret-value",
		CreatedBy: "test",
		CreatedAt: now,
	}))

	handler := coordinator.NewHandler(coordinator.HandlerConfig{SecretStore: secretStore})

	resp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:      "MY_SECRET",
		Ref:       "prod/my-secret",
		Workspace: "payments",
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-value", resp.GetValue())

	checkResp, err := handler.ResolveSecretReference(ctx, &coordinatorv1.ResolveSecretReferenceRequest{
		Name:      "MY_SECRET",
		Ref:       "prod/my-secret",
		Workspace: "payments",
		CheckOnly: true,
	})
	require.NoError(t, err)
	assert.Empty(t, checkResp.GetValue())
}
