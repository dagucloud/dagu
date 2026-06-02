// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/runtime"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	testhelper "github.com/dagucloud/dagu/internal/test"
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
			Description: new("Local development"),
			Protected:   new(true),
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

func TestRuntimeProfilesAPI_ProtectedProfileUseRequiresAdmin(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(
		t, server, adminToken, "profile-manager", "managerpass1", apigen.UserRoleManager,
	)
	operatorToken := createRuntimeProfileUserToken(
		t, server, adminToken, "profile-operator", "operatorpass1", apigen.UserRoleOperator,
	)

	dagName := "protected_profile_run_dag"
	spec := `
steps:
  - name: main
    run: echo runtime profile
`
	server.Client().Post("/api/v1/dags", apigen.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name: "local",
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name:      "prod",
		Protected: new(true),
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	localProfile := apigen.RuntimeProfileOverride("local")
	managerRunID := "manager-unprotected-profile"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &managerRunID,
		Profile:  &localProfile,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusOK).Send(t)

	operatorRunID := "operator-unprotected-profile"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &operatorRunID,
		Profile:  &localProfile,
	}).WithBearerToken(operatorToken).ExpectStatus(http.StatusOK).Send(t)

	protectedProfile := apigen.RuntimeProfileOverride("prod")
	forbiddenRunID := "manager-protected-profile"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &forbiddenRunID,
		Profile:  &protectedProfile,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	adminRunID := "admin-protected-profile"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &adminRunID,
		Profile:  &protectedProfile,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
}

func TestRuntimeProfilesAPI_DAGDefaultProfile(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(
		t, server, adminToken, "default-profile-manager", "managerpass1", apigen.UserRoleManager,
	)
	operatorToken := createRuntimeProfileUserToken(
		t, server, adminToken, "default-profile-operator", "operatorpass1", apigen.UserRoleOperator,
	)

	dagName := "default_profile_dag"
	spec := `
steps:
  - name: main
    run: echo default profile
`
	server.Client().Post("/api/v1/dags", apigen.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name: "local",
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)
	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name:      "prod",
		Protected: new(true),
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	localProfile := apigen.RuntimeProfileName("local")
	server.Client().Put(fmt.Sprintf("/api/v1/dags/%s/settings", dagName), apigen.UpdateDAGSettingsJSONRequestBody{
		Profile: &localProfile,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusOK).Send(t)

	var settings apigen.DAGSettings
	server.Client().Get(fmt.Sprintf("/api/v1/dags/%s/settings", dagName)).
		WithBearerToken(operatorToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &settings)
	require.NotNil(t, settings.Profile)
	assert.Equal(t, "local", *settings.Profile)

	protectedProfile := apigen.RuntimeProfileName("prod")
	server.Client().Put(fmt.Sprintf("/api/v1/dags/%s/settings", dagName), apigen.UpdateDAGSettingsJSONRequestBody{
		Profile: &protectedProfile,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Put(fmt.Sprintf("/api/v1/dags/%s/settings", dagName), apigen.UpdateDAGSettingsJSONRequestBody{
		Profile: &protectedProfile,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	defaultRunID := "uses-protected-default"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &defaultRunID,
	}).WithBearerToken(operatorToken).ExpectStatus(http.StatusOK).Send(t)
	defaultStatus := waitForStoredDAGRunStatus(t, server, dagName, defaultRunID, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.ProfileName == "prod"
	})
	assert.Equal(t, "prod", defaultStatus.ProfileName)

	noProfile := apigen.RuntimeProfileOverride("")
	noProfileRunID := "bypasses-default-profile"
	server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), apigen.ExecuteDAGJSONRequestBody{
		DagRunId: &noProfileRunID,
		Profile:  &noProfile,
	}).WithBearerToken(operatorToken).ExpectStatus(http.StatusOK).Send(t)
	noProfileStatus := waitForStoredDAGRunStatus(t, server, dagName, noProfileRunID, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.DAGRunID == noProfileRunID
	})
	assert.Empty(t, noProfileStatus.ProfileName)
}

func TestRuntimeProfilesAPI_ProtectedProfileManagementRequiresAdmin(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(
		t, server, adminToken, "profile-manager", "managerpass1", apigen.UserRoleManager,
	)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name:      "manager-protected",
		Protected: new(true),
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name: "local",
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Patch("/api/v1/profiles/local", apigen.UpdateRuntimeProfileJSONRequestBody{
		Protected: new(true),
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name:      "prod",
		Protected: new(true),
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	listResp := server.Client().Get("/api/v1/profiles").
		WithBearerToken(managerToken).ExpectStatus(http.StatusOK).Send(t)
	var list apigen.RuntimeProfileListResponse
	listResp.Unmarshal(t, &list)
	require.Len(t, list.Profiles, 1)
	assert.Equal(t, "local", list.Profiles[0].Name)

	server.Client().Get("/api/v1/profiles/prod").
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Get("/api/v1/profiles/prod").
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	server.Client().Put("/api/v1/profiles/prod/variables/API_TOKEN", apigen.SetRuntimeProfileVariableJSONRequestBody{
		Value: "manager-value",
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Put("/api/v1/profiles/prod/variables/API_TOKEN", apigen.SetRuntimeProfileVariableJSONRequestBody{
		Value: "admin-value",
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	server.Client().Delete("/api/v1/profiles/prod/entries/API_TOKEN").
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Delete("/api/v1/profiles/prod").
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Delete("/api/v1/profiles/prod/entries/API_TOKEN").
		WithBearerToken(adminToken).ExpectStatus(http.StatusNoContent).Send(t)
}

func TestRuntimeProfilesAPI_RetryInheritsProtectedProfileWithoutAdmin(t *testing.T) {
	server := setupBuiltinAuthServer(t, func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "protected-profile-retry", MaxActiveRuns: 1},
		}
	})
	adminToken := getAdminToken(t, server)
	operatorToken := createRuntimeProfileUserToken(
		t, server, adminToken, "protected-profile-retry-operator", "operatorpass1", apigen.UserRoleOperator,
	)

	dagName := "protected_profile_retry_dag"
	spec := `
queue: protected-profile-retry
steps:
  - name: main
    run: echo retry inherited profile
`
	server.Client().Post("/api/v1/dags", apigen.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/profiles", apigen.CreateRuntimeProfileJSONRequestBody{
		Name:      "prod",
		Protected: new(true),
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	protectedProfile := apigen.RuntimeProfileName("prod")
	server.Client().Put(fmt.Sprintf("/api/v1/dags/%s/settings", dagName), apigen.UpdateDAGSettingsJSONRequestBody{
		Profile: &protectedProfile,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	dag, err := server.DAGStore.GetMetadata(server.Context, dagName)
	require.NoError(t, err)

	seedLatestDAGRunStatus(t, server, dag, "protected-profile-source-run", core.Failed, seedDAGRunStatusOptions{
		errorText:   "source run failed",
		profileName: "prod",
	})

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/retry", dagName, "protected-profile-source-run"),
		apigen.RetryDAGRunJSONRequestBody{DagRunId: "protected-profile-source-run"},
	).WithBearerToken(operatorToken).ExpectStatus(http.StatusOK).Send(t)

	attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, "protected-profile-source-run"))
	require.NoError(t, err)

	status, err := attempt.ReadStatus(server.Context)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	assert.Equal(t, "prod", status.ProfileName)
}

func TestRuntimeProfilesAPI_SetSecretDeletesCreatedSecretWhenProfileUpdateFails(t *testing.T) {
	ctx := context.Background()
	_, profileStore, secretStore := newRuntimeProfilesTestAPI(t)

	prof, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "test"}, nowForRuntimeProfileTest())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))

	api := newRuntimeProfilesTestAPIWithStores(t, failingUpdateProfileStore{
		Store: profileStore,
		err:   fmt.Errorf("forced profile update failure"),
	}, secretStore)

	plainSecret := "new-value"
	_, err = api.SetRuntimeProfileSecret(ctx, apigen.SetRuntimeProfileSecretRequestObject{
		ProfileName: "local",
		Key:         "API_TOKEN",
		Body: &apigen.SetRuntimeProfileSecretRequest{
			Value: &plainSecret,
		},
	})
	require.Error(t, err)

	_, err = secretStore.GetByRef(ctx, "", profile.SecretRef("local", "API_TOKEN"))
	require.ErrorIs(t, err, secretpkg.ErrNotFound)
}

func TestRuntimeProfilesAPI_SetSecretDoesNotRotateExistingSecretWhenProfileUpdateFails(t *testing.T) {
	ctx := context.Background()
	_, profileStore, secretStore := newRuntimeProfilesTestAPI(t)

	prof, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "test"}, nowForRuntimeProfileTest())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))

	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:    "",
		Ref:          profile.SecretRef("local", "API_TOKEN"),
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "test",
	}, nowForRuntimeProfileTest())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value:     "old-value",
		CreatedBy: "test",
	}))

	api := newRuntimeProfilesTestAPIWithStores(t, failingUpdateProfileStore{
		Store: profileStore,
		err:   fmt.Errorf("forced profile update failure"),
	}, secretStore)

	plainSecret := "new-value"
	_, err = api.SetRuntimeProfileSecret(ctx, apigen.SetRuntimeProfileSecretRequestObject{
		ProfileName: "local",
		Key:         "API_TOKEN",
		Body: &apigen.SetRuntimeProfileSecretRequest{
			Value: &plainSecret,
		},
	})
	require.Error(t, err)

	resolved, _, err := secretStore.ResolveValue(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, "old-value", resolved)
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

	return newRuntimeProfilesTestAPIWithStores(t, profileStore, secretStore), profileStore, secretStore
}

func newRuntimeProfilesTestAPIWithStores(t *testing.T, profileStore profile.Store, secretStore secretpkg.Store) *apiv1.API {
	t.Helper()

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
	)
}

func createRuntimeProfileUserToken(t *testing.T, server testhelper.Server, adminToken, username, password string, role apigen.UserRole) string {
	t.Helper()

	server.Client().Post("/api/v1/users", apigen.CreateUserRequest{
		Username: username,
		Password: password,
		Role:     role,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	resp := server.Client().Post("/api/v1/auth/login", apigen.LoginRequest{
		Username: username,
		Password: password,
	}).ExpectStatus(http.StatusOK).Send(t)

	var login apigen.LoginResponse
	resp.Unmarshal(t, &login)
	require.NotEmpty(t, login.Token)
	return login.Token
}

func nowForRuntimeProfileTest() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

type failingUpdateProfileStore struct {
	profile.Store
	err error
}

func (s failingUpdateProfileStore) Update(context.Context, *profile.Profile) error {
	return s.err
}
