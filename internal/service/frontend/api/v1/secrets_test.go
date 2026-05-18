// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"encoding/json"
	"testing"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/filesecret"
	"github.com/dagucloud/dagu/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretsAPI_CreateWriteListDoesNotReturnPlaintext(t *testing.T) {
	ctx := context.Background()
	api, store := newSecretsTestAPI(t)

	value := "super-secret-value"
	resp, err := api.CreateSecret(ctx, apigen.CreateSecretRequestObject{
		Body: &apigen.CreateSecretRequest{
			Ref:          "prod/db-password",
			ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
			Value:        &value,
		},
	})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "global", created.Workspace)
	assert.Equal(t, 1, created.CurrentVersion)
	assert.True(t, created.HasValue)

	payload := mustJSON(t, created)
	assert.NotContains(t, payload, value)

	resolved, version, err := store.ResolveValue(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, value, resolved)
	assert.Equal(t, 1, version.Version)

	rotated := "rotated-secret-value"
	writeResp, err := api.WriteSecretVersion(ctx, apigen.WriteSecretVersionRequestObject{
		SecretId: created.Id,
		Body: &apigen.WriteSecretVersionRequest{
			Value: &rotated,
		},
	})
	require.NoError(t, err)
	written, ok := writeResp.(apigen.WriteSecretVersion200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 2, written.CurrentVersion)
	assert.NotContains(t, mustJSON(t, written), rotated)

	listResp, err := api.ListSecrets(ctx, apigen.ListSecretsRequestObject{})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListSecrets200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Secrets, 1)
	assert.Equal(t, created.Id, listed.Secrets[0].Id)
	listPayload := mustJSON(t, listed)
	assert.NotContains(t, listPayload, value)
	assert.NotContains(t, listPayload, rotated)
}

func TestSecretsAPI_CreateRejectsDuplicateWorkspaceRef(t *testing.T) {
	ctx := context.Background()
	api, _ := newSecretsTestAPI(t)
	value := "secret-value"
	body := &apigen.CreateSecretRequest{
		Ref:          "prod/api-key",
		ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
		Value:        &value,
	}

	resp, err := api.CreateSecret(ctx, apigen.CreateSecretRequestObject{Body: body})
	require.NoError(t, err)
	_, ok := resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)

	resp, err = api.CreateSecret(ctx, apigen.CreateSecretRequestObject{Body: body})
	require.NoError(t, err)
	_, ok = resp.(apigen.CreateSecret409JSONResponse)
	require.True(t, ok)
}

func TestSecretsAPI_ListsSingleSecretScopeOnly(t *testing.T) {
	ctx := context.Background()
	api, _ := newSecretsTestAPI(t)
	value := "secret-value"
	globalWorkspace := "global"
	globalBody := &apigen.CreateSecretRequest{
		Workspace:    &globalWorkspace,
		Ref:          "prod/global-key",
		ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
		Value:        &value,
	}
	paymentsWorkspace := "payments"
	paymentsBody := &apigen.CreateSecretRequest{
		Workspace:    &paymentsWorkspace,
		Ref:          "prod/payments-key",
		ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
		Value:        &value,
	}

	resp, err := api.CreateSecret(ctx, apigen.CreateSecretRequestObject{Body: globalBody})
	require.NoError(t, err)
	_, ok := resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)
	resp, err = api.CreateSecret(ctx, apigen.CreateSecretRequestObject{Body: paymentsBody})
	require.NoError(t, err)
	_, ok = resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)

	listResp, err := api.ListSecrets(ctx, apigen.ListSecretsRequestObject{
		Params: apigen.ListSecretsParams{Workspace: &globalWorkspace},
	})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListSecrets200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Secrets, 1)
	assert.Equal(t, "global", listed.Secrets[0].Workspace)
	assert.Equal(t, "prod/global-key", listed.Secrets[0].Ref)

	listResp, err = api.ListSecrets(ctx, apigen.ListSecretsRequestObject{
		Params: apigen.ListSecretsParams{Workspace: &paymentsWorkspace},
	})
	require.NoError(t, err)
	listed, ok = listResp.(apigen.ListSecrets200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Secrets, 1)
	assert.Equal(t, "payments", listed.Secrets[0].Workspace)
	assert.Equal(t, "prod/payments-key", listed.Secrets[0].Ref)
}

func TestSecretsAPI_CreatesGlobalAndWorkspaceScopesSeparately(t *testing.T) {
	ctx := context.Background()
	api, _ := newSecretsTestAPI(t)
	value := "secret-value"
	ref := "prod/shared-key"

	resp, err := api.CreateSecret(ctx, apigen.CreateSecretRequestObject{
		Body: &apigen.CreateSecretRequest{
			Ref:          ref,
			ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
			Value:        &value,
		},
	})
	require.NoError(t, err)
	globalCreated, ok := resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "global", globalCreated.Workspace)

	paymentsWorkspace := "payments"
	resp, err = api.CreateSecret(ctx, apigen.CreateSecretRequestObject{
		Body: &apigen.CreateSecretRequest{
			Workspace:    &paymentsWorkspace,
			Ref:          ref,
			ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
			Value:        &value,
		},
	})
	require.NoError(t, err)
	paymentsCreated, ok := resp.(apigen.CreateSecret201JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "payments", paymentsCreated.Workspace)

	listResp, err := api.ListSecrets(ctx, apigen.ListSecretsRequestObject{})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListSecrets200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Secrets, 1)
	assert.Equal(t, "global", listed.Secrets[0].Workspace)

	listResp, err = api.ListSecrets(ctx, apigen.ListSecretsRequestObject{
		Params: apigen.ListSecretsParams{Workspace: &paymentsWorkspace},
	})
	require.NoError(t, err)
	listed, ok = listResp.(apigen.ListSecrets200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Secrets, 1)
	assert.Equal(t, "payments", listed.Secrets[0].Workspace)
}

func TestSecretsAPI_RejectsUnsupportedWorkspaceSelectors(t *testing.T) {
	ctx := context.Background()
	api, _ := newSecretsTestAPI(t)
	for _, workspaceName := range []string{"all", "default"} {
		t.Run(workspaceName, func(t *testing.T) {
			resp, err := api.ListSecrets(ctx, apigen.ListSecretsRequestObject{
				Params: apigen.ListSecretsParams{Workspace: &workspaceName},
			})
			assert.Nil(t, resp)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "workspace cannot be "+workspaceName)
		})
	}
}

func TestSecretsAPI_CreateRejectsDefaultWorkspaceSelector(t *testing.T) {
	ctx := context.Background()
	api, _ := newSecretsTestAPI(t)
	value := "secret-value"
	defaultWorkspace := "default"

	resp, err := api.CreateSecret(ctx, apigen.CreateSecretRequestObject{
		Body: &apigen.CreateSecretRequest{
			Workspace:    &defaultWorkspace,
			Ref:          "prod/db-password",
			ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged,
			Value:        &value,
		},
	})
	require.NoError(t, err)
	rejected, ok := resp.(apigen.CreateSecret400JSONResponse)
	require.True(t, ok)
	assert.Contains(t, rejected.Message, "workspace cannot be default")
}

func newSecretsTestAPI(t *testing.T) (*apiv1.API, *filesecret.Store) {
	t.Helper()

	dataDir := t.TempDir()
	store, err := filesecret.NewFromDataDir(dataDir)
	require.NoError(t, err)

	cfg := &config.Config{
		Paths: config.PathsConfig{
			DataDir: dataDir,
		},
	}
	return apiv1.New(
		nil,
		nil,
		nil,
		nil,
		runtime.Manager{},
		cfg,
		nil,
		nil,
		prometheus.NewRegistry(),
		nil,
		apiv1.WithSecretStore(store),
	), store
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
