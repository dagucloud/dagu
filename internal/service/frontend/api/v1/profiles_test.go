// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"testing"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/runtime"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeProfilesAPI_CreateSetEntriesDoesNotReturnPlaintext(t *testing.T) {
	ctx := context.Background()
	api, profileStore, secretStore := newRuntimeProfilesTestAPI(t)

	resp, err := api.CreateRuntimeProfile(ctx, apigen.CreateRuntimeProfileRequestObject{
		Body: &apigen.CreateRuntimeProfileRequest{
			Name:        "local",
			Description: ptrTo("Local development"),
			Protected:   ptrTo(true),
		},
	})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateRuntimeProfile201JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "local", created.Name)
	assert.Equal(t, apigen.RuntimeProfileStatusActive, created.Status)
	assert.True(t, created.Protected)

	variableResp, err := api.SetRuntimeProfileVariable(ctx, apigen.SetRuntimeProfileVariableRequestObject{
		ProfileName: "local",
		Key:         "LOG_LEVEL",
		Body: &apigen.SetRuntimeProfileVariableRequest{
			Value: "debug",
		},
	})
	require.NoError(t, err)
	_, ok = variableResp.(apigen.SetRuntimeProfileVariable200JSONResponse)
	require.True(t, ok)

	plainSecret := "profile-secret-value"
	secretResp, err := api.SetRuntimeProfileSecret(ctx, apigen.SetRuntimeProfileSecretRequestObject{
		ProfileName: "local",
		Key:         "DB_PASSWORD",
		Body: &apigen.SetRuntimeProfileSecretRequest{
			Value: &plainSecret,
		},
	})
	require.NoError(t, err)
	withSecret, ok := secretResp.(apigen.SetRuntimeProfileSecret200JSONResponse)
	require.True(t, ok)
	assert.NotContains(t, mustJSON(t, withSecret), plainSecret)

	require.Len(t, withSecret.Entries, 2)
	entryByKey := map[string]apigen.RuntimeProfileEntryResponse{}
	for _, entry := range withSecret.Entries {
		entryByKey[entry.Key] = entry
	}
	assert.Equal(t, apigen.RuntimeProfileEntryKindVariable, entryByKey["LOG_LEVEL"].Kind)
	require.NotNil(t, entryByKey["LOG_LEVEL"].Value)
	assert.Equal(t, "debug", *entryByKey["LOG_LEVEL"].Value)
	assert.Equal(t, apigen.RuntimeProfileEntryKindSecret, entryByKey["DB_PASSWORD"].Kind)
	assert.Nil(t, entryByKey["DB_PASSWORD"].Value)
	require.NotNil(t, entryByKey["DB_PASSWORD"].SecretId)

	resolved, version, err := secretStore.ResolveValue(ctx, *entryByKey["DB_PASSWORD"].SecretId)
	require.NoError(t, err)
	assert.Equal(t, plainSecret, resolved)
	assert.Equal(t, 1, version.Version)

	stored, err := profileStore.GetByName(ctx, "local")
	require.NoError(t, err)
	require.Len(t, stored.Entries, 2)

	listResp, err := api.ListRuntimeProfiles(ctx, apigen.ListRuntimeProfilesRequestObject{})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListRuntimeProfiles200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Profiles, 1)
	assert.NotContains(t, mustJSON(t, listed), plainSecret)
}

func TestRuntimeProfilesAPI_RejectsReservedDaguKey(t *testing.T) {
	ctx := context.Background()
	api, _, _ := newRuntimeProfilesTestAPI(t)

	resp, err := api.CreateRuntimeProfile(ctx, apigen.CreateRuntimeProfileRequestObject{
		Body: &apigen.CreateRuntimeProfileRequest{Name: "local"},
	})
	require.NoError(t, err)
	_, ok := resp.(apigen.CreateRuntimeProfile201JSONResponse)
	require.True(t, ok)

	setResp, err := api.SetRuntimeProfileVariable(ctx, apigen.SetRuntimeProfileVariableRequestObject{
		ProfileName: "local",
		Key:         "DAGU_HOME",
		Body: &apigen.SetRuntimeProfileVariableRequest{
			Value: "/tmp/dagu",
		},
	})
	require.NoError(t, err)
	rejected, ok := setResp.(apigen.SetRuntimeProfileVariable400JSONResponse)
	require.True(t, ok)
	assert.Contains(t, rejected.Message, "reserved")
}

func newRuntimeProfilesTestAPI(t *testing.T) (*apiv1.API, profile.Store, secretpkg.Store) {
	t.Helper()

	backend := testutil.NewMemoryBackend()
	profileStore, err := persiststore.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	enc, err := crypto.NewEncryptor("test-key-for-runtime-profiles")
	require.NoError(t, err)
	secretStore, err := persiststore.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)

	cfg := &config.Config{}
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
		apiv1.WithProfileStore(profileStore),
		apiv1.WithSecretStore(secretStore),
	), profileStore, secretStore
}

func ptrTo[T any](value T) *T {
	return &value
}
