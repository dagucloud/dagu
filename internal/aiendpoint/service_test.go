// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	keyOne = "sk-ant-api03-0123456789abcdefghij"
	keyTwo = "sk-ant-api03-ZYXWVUTSRQPONMLKJIHG"
)

type fixture struct {
	svc     *aiendpoint.Service
	secrets *store.SecretStore
	backend *testutil.MemoryBackend
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	secrets, err := store.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	endpoints, err := store.NewAIEndpointStore(backend.Collection("ai_endpoints"))
	require.NoError(t, err)
	svc, err := aiendpoint.NewService(endpoints, secrets)
	require.NoError(t, err)
	return &fixture{svc: svc, secrets: secrets, backend: backend}
}

func globalInput(name, key string, workspaces, dags []string) aiendpoint.CreateInput {
	return aiendpoint.CreateInput{
		Name:     name,
		Preset:   "anthropic",
		Protocol: aiendpoint.ProtocolAnthropic,
		Scope:    aiendpoint.GlobalScope(),
		Allow:    aiendpoint.Allow{Workspaces: workspaces, DAGs: dags},
		Key:      key,
		Actor:    "admin",
	}
}

func workspaceInput(name, key, workspace string, dags []string) aiendpoint.CreateInput {
	in := globalInput(name, key, nil, dags)
	in.Scope = aiendpoint.WorkspaceScope(workspace)
	return in
}

func TestCreateStoresTheKeyAsManagedSecret(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	ep, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"fix-bug"}))
	require.NoError(t, err)

	// The preset gives the defaults.
	assert.Equal(t, "https://api.anthropic.com", ep.BaseURL)
	assert.Equal(t, aiendpoint.AuthXAPIKey, ep.Auth)
	// The display values come from the key.
	prefix, fingerprint := aiendpoint.KeyDisplay(keyOne)
	assert.Equal(t, prefix, ep.KeyPrefix)
	assert.Equal(t, fingerprint, ep.KeyFingerprint)
	assert.Len(t, ep.KeyPrefix, 3)
	assert.False(t, ep.KeyUpdatedAt.IsZero())

	// The key is a managed secret of the global scope with the ref of the spec.
	sec, err := f.secrets.GetByRef(ctx, "", "ai/endpoint/cle1")
	require.NoError(t, err)
	assert.Equal(t, secret.ProviderDaguManaged, sec.ProviderType)
	assert.Equal(t, secret.GlobalWorkspace, sec.Workspace)
	assert.Equal(t, sec.ID, ep.SecretID)

	// No record of the endpoint store holds the key.
	for _, col := range []string{"ai_endpoints"} {
		page, err := f.backend.Collection(col).List(ctx, persis.ListQuery{})
		require.NoError(t, err)
		require.NotEmpty(t, page.Records)
		for _, rec := range page.Records {
			assert.NotContains(t, string(rec.Data), keyOne)
		}
	}
	page, err := f.backend.Collection("secrets").List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	for _, rec := range page.Records {
		assert.NotContains(t, string(rec.Data), keyOne, "the secret store encrypts the value")
	}

	// The metadata JSON has no key either.
	data, err := json.Marshal(ep)
	require.NoError(t, err)
	assert.NotContains(t, string(data), keyOne)
}

func TestKeyIsReadByTheInternalMethodOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	ep, err := f.svc.ResolveEndpoint(ctx, "cle1", "any-dag", "")
	require.NoError(t, err)
	assert.Equal(t, created.SecretID, ep.SecretID)

	key, err := f.svc.ReadKey(ctx, ep)
	require.NoError(t, err)
	assert.Equal(t, keyOne, key)
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	valid := func() aiendpoint.CreateInput {
		return globalInput("cle1", keyOne, []string{"*"}, []string{"*"})
	}
	for name, mutate := range map[string]func(*aiendpoint.CreateInput){
		"bad name":                func(in *aiendpoint.CreateInput) { in.Name = "Cle_1" },
		"long description":        func(in *aiendpoint.CreateInput) { in.Description = strings.Repeat("d", 201) },
		"unknown preset":          func(in *aiendpoint.CreateInput) { in.Preset = "nope" },
		"preset without the row":  func(in *aiendpoint.CreateInput) { in.Protocol = aiendpoint.ProtocolOpenAI },
		"empty protocol":          func(in *aiendpoint.CreateInput) { in.Protocol = "" },
		"http base url":           func(in *aiendpoint.CreateInput) { in.BaseURL = "http://api.example.com" },
		"base url with user":      func(in *aiendpoint.CreateInput) { in.BaseURL = "https://u:p@api.example.com" },
		"base url with query":     func(in *aiendpoint.CreateInput) { in.BaseURL = "https://api.example.com/?a=b" },
		"base url with fragment":  func(in *aiendpoint.CreateInput) { in.BaseURL = "https://api.example.com/#x" },
		"relative base url":       func(in *aiendpoint.CreateInput) { in.BaseURL = "api.example.com" },
		"ftp base url":            func(in *aiendpoint.CreateInput) { in.BaseURL = "ftp://api.example.com" },
		"unknown auth":            func(in *aiendpoint.CreateInput) { in.Auth = "basic" },
		"models url on a preset":  func(in *aiendpoint.CreateInput) { in.ModelsURL = "https://api.example.com/models" },
		"no key":                  func(in *aiendpoint.CreateInput) { in.Key = "" },
		"key with control char":   func(in *aiendpoint.CreateInput) { in.Key = "sk\n1" },
		"workspaces missing":      func(in *aiendpoint.CreateInput) { in.Allow.Workspaces = nil },
		"dags missing":            func(in *aiendpoint.CreateInput) { in.Allow.DAGs = nil },
		"wildcard with a name":    func(in *aiendpoint.CreateInput) { in.Allow.DAGs = []string{"*", "a"} },
		"bad workspace name":      func(in *aiendpoint.CreateInput) { in.Allow.Workspaces = []string{"a/b"} },
		"bad dag name":            func(in *aiendpoint.CreateInput) { in.Allow.DAGs = []string{"a b"} },
		"empty dag name":          func(in *aiendpoint.CreateInput) { in.Allow.DAGs = []string{""} },
		"workspaces in workspace": func(in *aiendpoint.CreateInput) { in.Scope = aiendpoint.WorkspaceScope("team") },
	} {
		in := valid()
		mutate(&in)
		_, err := f.svc.Create(ctx, in)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, name)
		assert.NotContains(t, err.Error(), keyOne, name)
	}

	// None of the failed calls left a record.
	list, err := f.svc.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
	secrets, err := f.secrets.List(ctx, secret.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets)
}

func TestCreateOAuthNeedsTheAnthropicProtocol(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	in := globalInput("cle1", keyOne, []string{"*"}, []string{"*"})
	in.Auth = aiendpoint.AuthAnthropicOAuth
	ep, err := f.svc.Create(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, aiendpoint.AuthAnthropicOAuth, ep.Auth)

	bad := globalInput("cle2", keyOne, []string{"*"}, []string{"*"})
	bad.Preset, bad.Protocol, bad.Auth = "openai", aiendpoint.ProtocolOpenAI, aiendpoint.AuthAnthropicOAuth
	_, err = f.svc.Create(ctx, bad)
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
}

func TestCustomPresetNeedsAddressAndAuth(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	in := globalInput("mine", keyOne, []string{"*"}, []string{"*"})
	in.Preset, in.Protocol = aiendpoint.PresetCustom, aiendpoint.ProtocolOpenAI
	_, err := f.svc.Create(ctx, in)
	require.ErrorIs(t, err, aiendpoint.ErrInvalid, "base_url is required")

	in.BaseURL = "http://gateway.internal:8080/v1"
	_, err = f.svc.Create(ctx, in)
	require.ErrorIs(t, err, aiendpoint.ErrInvalid, "auth is required")

	in.Auth = aiendpoint.AuthBearer
	in.ModelsURL = "http://gateway.internal:8080/v1/models"
	ep, err := f.svc.Create(ctx, in)
	require.NoError(t, err, "custom accepts http and a models URL")
	assert.Equal(t, in.ModelsURL, ep.ModelsURL)
}

func TestLocalModelAcceptsNoKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	in := globalInput("local", "", []string{"*"}, []string{"*"})
	in.Preset, in.Protocol = "ollama", aiendpoint.ProtocolOpenAI
	ep, err := f.svc.Create(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:11434/v1", ep.BaseURL)
	assert.False(t, ep.HasKey())
	assert.Empty(t, ep.KeyFingerprint)

	key, err := f.svc.ReadKey(ctx, *ep)
	require.NoError(t, err)
	assert.Empty(t, key)

	// A key can come later, by a rotation.
	rotated, err := f.svc.RotateKey(ctx, ep.Scope, ep.Name, keyOne, "admin")
	require.NoError(t, err)
	assert.True(t, rotated.HasKey())
	key, err = f.svc.ReadKey(ctx, *rotated)
	require.NoError(t, err)
	assert.Equal(t, keyOne, key)

	// A preset with no local flag has no such case.
	missing := aiendpoint.Endpoint{Name: "x", Preset: "anthropic", Protocol: aiendpoint.ProtocolAnthropic}
	_, err = f.svc.ReadKey(ctx, missing)
	assert.Equal(t, aiendpoint.ReasonKeyMissing, aiendpoint.ReasonOf(err))
}

func TestNameIsUniqueInItsScope(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	_, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, globalInput("cle1", keyTwo, []string{"*"}, []string{"*"}))
	require.ErrorIs(t, err, aiendpoint.ErrAlreadyExists)

	// The same name in a workspace is a different endpoint, with its own key.
	ws, err := f.svc.Create(ctx, workspaceInput("cle1", keyTwo, "team", []string{"*"}))
	require.NoError(t, err)
	global, err := f.svc.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.NotEqual(t, global.SecretID, ws.SecretID)
	assert.NotEqual(t, global.KeyFingerprint, ws.KeyFingerprint)

	sec, err := f.secrets.GetByRef(ctx, "team", "ai/endpoint/cle1")
	require.NoError(t, err)
	assert.Equal(t, ws.SecretID, sec.ID)

	_, err = f.svc.Create(ctx, workspaceInput("cle1", keyTwo, "team", []string{"*"}))
	require.ErrorIs(t, err, aiendpoint.ErrAlreadyExists)
}

func TestRotateKeyChangesTheFingerprintAndKeepsTheOldVersion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	rotated, err := f.svc.RotateKey(ctx, created.Scope, "cle1", keyTwo, "admin2")
	require.NoError(t, err)
	assert.NotEqual(t, created.KeyFingerprint, rotated.KeyFingerprint)
	_, want := aiendpoint.KeyDisplay(keyTwo)
	assert.Equal(t, want, rotated.KeyFingerprint)
	assert.Equal(t, created.SecretID, rotated.SecretID, "a rotation writes a new version of the same secret")
	assert.Equal(t, "admin2", rotated.UpdatedBy)

	key, err := f.svc.ReadKey(ctx, *rotated)
	require.NoError(t, err)
	assert.Equal(t, keyTwo, key)

	meta, err := f.secrets.GetCurrentVersion(ctx, created.SecretID)
	require.NoError(t, err)
	assert.Equal(t, 2, meta.Version)

	_, err = f.svc.RotateKey(ctx, created.Scope, "cle1", "bad\nkey", "admin")
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
	_, err = f.svc.RotateKey(ctx, created.Scope, "missing", keyTwo, "admin")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
}

func TestUpdateChangesOnlyTheFieldsThatAreNotKeyAndScope(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"fix-bug"}))
	require.NoError(t, err)

	description := "Main key"
	baseURL := "https://proxy.example.com"
	updated, err := f.svc.Update(ctx, created.Scope, "cle1", aiendpoint.UpdateInput{
		Description: &description,
		BaseURL:     &baseURL,
		Allow:       &aiendpoint.Allow{Workspaces: []string{"team"}, DAGs: []string{"fix-bug", "review"}},
		Actor:       "admin2",
	})
	require.NoError(t, err)
	assert.Equal(t, "Main key", updated.Description)
	assert.Equal(t, baseURL, updated.BaseURL)
	assert.Equal(t, []string{"fix-bug", "review"}, updated.Allow.DAGs)
	assert.Equal(t, created.KeyFingerprint, updated.KeyFingerprint, "an update keeps the key")
	assert.Equal(t, created.SecretID, updated.SecretID)
	assert.Equal(t, "anthropic", updated.Preset)

	bad := "http://insecure.example.com"
	_, err = f.svc.Update(ctx, created.Scope, "cle1", aiendpoint.UpdateInput{BaseURL: &bad})
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
	empty := ""
	_, err = f.svc.Update(ctx, created.Scope, "cle1", aiendpoint.UpdateInput{BaseURL: &empty})
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
	models := "https://x.example.com/models"
	_, err = f.svc.Update(ctx, created.Scope, "cle1", aiendpoint.UpdateInput{ModelsURL: &models})
	require.ErrorIs(t, err, aiendpoint.ErrInvalid, "models_url is valid with custom only")

	_, err = f.svc.Update(ctx, aiendpoint.GlobalScope(), "missing", aiendpoint.UpdateInput{})
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)

	// A failed update leaves the record as it was.
	got, err := f.svc.Get(ctx, created.Scope, "cle1")
	require.NoError(t, err)
	assert.Equal(t, baseURL, got.BaseURL)
}

func TestDeleteRemovesTheEndpointAndItsKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	require.NoError(t, f.svc.Delete(ctx, created.Scope, "cle1"))

	_, err = f.svc.Get(ctx, created.Scope, "cle1")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
	_, err = f.secrets.GetByID(ctx, created.SecretID)
	require.ErrorIs(t, err, secret.ErrNotFound)
	_, err = f.svc.ResolveEndpoint(ctx, "cle1", "any", "")
	assert.Equal(t, aiendpoint.ReasonUnknown, aiendpoint.ReasonOf(err))

	require.ErrorIs(t, f.svc.Delete(ctx, created.Scope, "cle1"), aiendpoint.ErrNotFound)

	// The name is free again.
	_, err = f.svc.Create(ctx, globalInput("cle1", keyTwo, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
}

// failingModels is a models store whose Delete fails.
type failingModels struct{ aiendpoint.ModelsStore }

func (failingModels) Delete(context.Context, aiendpoint.Scope, string) error {
	return errors.New("disk full")
}

// A models list that cannot be deleted does not keep the key of a deleted
// endpoint in the secret store.
func TestDeleteRemovesTheKeyWhenTheModelsListStays(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	models, err := store.NewAIEndpointModelsStore(f.backend.Collection("models"))
	require.NoError(t, err)
	reader, err := aiendpoint.NewModelsReader(http.DefaultClient, time.Second)
	require.NoError(t, err)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	require.NoError(t, f.svc.EnableModels(failingModels{models}, reader))

	require.NoError(t, f.svc.Delete(ctx, created.Scope, "cle1"))
	_, err = f.secrets.GetByID(ctx, created.SecretID)
	require.ErrorIs(t, err, secret.ErrNotFound)
}

// An administrator can delete the secret of a key through the secret routes. A
// later rotation makes the secret again, so the endpoint can be repaired.
func TestRotateKeyMakesADeletedSecretAgain(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	require.NoError(t, f.secrets.Delete(ctx, created.SecretID))

	rotated, err := f.svc.RotateKey(ctx, created.Scope, "cle1", keyTwo, "admin")
	require.NoError(t, err)
	assert.NotEqual(t, created.SecretID, rotated.SecretID)
	key, err := f.svc.ReadKey(ctx, *rotated)
	require.NoError(t, err)
	assert.Equal(t, keyTwo, key)
}

func TestResolveEndpointUseRule(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	_, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"ops"}, []string{"fix-bug"}))
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, globalInput("open", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	for _, tc := range []struct {
		name, endpoint, dag, workspace string
		reason                         aiendpoint.Reason
	}{
		{"allowed", "cle1", "fix-bug", "ops", ""},
		{"dag not listed", "cle1", "other", "ops", aiendpoint.ReasonForbidden},
		{"workspace not listed", "cle1", "fix-bug", "dev", aiendpoint.ReasonForbidden},
		{"no workspace and a named list", "cle1", "fix-bug", "", aiendpoint.ReasonForbidden},
		{"wildcards allow all", "open", "any", "", ""},
		{"wildcards allow a workspace", "open", "any", "dev", ""},
		{"unknown name", "nope", "fix-bug", "ops", aiendpoint.ReasonUnknown},
		{"invalid name", "Not A Name", "fix-bug", "ops", aiendpoint.ReasonUnknown},
	} {
		ep, err := f.svc.ResolveEndpoint(ctx, tc.endpoint, tc.dag, tc.workspace)
		if tc.reason == "" {
			require.NoError(t, err, tc.name)
			assert.Equal(t, tc.endpoint, ep.Name, tc.name)
			continue
		}
		require.Error(t, err, tc.name)
		assert.Equal(t, tc.reason, aiendpoint.ReasonOf(err), tc.name)
		assert.True(t, errors.Is(err, &aiendpoint.Error{Reason: tc.reason}), tc.name)
		assert.NotContains(t, err.Error(), keyOne, tc.name)
	}

	_, err = f.svc.ResolveEndpoint(ctx, "cle1", "other", "ops")
	assert.Contains(t, err.Error(), `endpoint_forbidden: the DAG "other" is not in the allow list`)
}

func TestWorkspaceEndpointHidesTheGlobalOne(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	global, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	ws, err := f.svc.Create(ctx, workspaceInput("cle1", keyTwo, "team", []string{"only-this"}))
	require.NoError(t, err)

	// A DAG of the workspace sees the workspace endpoint.
	got, err := f.svc.ResolveEndpoint(ctx, "cle1", "only-this", "team")
	require.NoError(t, err)
	assert.Equal(t, ws.SecretID, got.SecretID)

	// The workspace endpoint hides the global one, even for a DAG that it forbids.
	_, err = f.svc.ResolveEndpoint(ctx, "cle1", "another", "team")
	assert.Equal(t, aiendpoint.ReasonForbidden, aiendpoint.ReasonOf(err))

	// A DAG of another workspace, or with none, sees the global endpoint.
	got, err = f.svc.ResolveEndpoint(ctx, "cle1", "another", "other-team")
	require.NoError(t, err)
	assert.Equal(t, global.SecretID, got.SecretID)
	got, err = f.svc.ResolveEndpoint(ctx, "cle1", "another", "")
	require.NoError(t, err)
	assert.Equal(t, global.SecretID, got.SecretID)

	// A workspace endpoint alone is unknown to the other workspaces and to a DAG with no workspace.
	_, err = f.svc.Create(ctx, workspaceInput("only-ws", keyTwo, "team", []string{"*"}))
	require.NoError(t, err)
	_, err = f.svc.ResolveEndpoint(ctx, "only-ws", "x", "other-team")
	assert.Equal(t, aiendpoint.ReasonUnknown, aiendpoint.ReasonOf(err))
	_, err = f.svc.ResolveEndpoint(ctx, "only-ws", "x", "")
	assert.Equal(t, aiendpoint.ReasonUnknown, aiendpoint.ReasonOf(err))
}

func TestChoicesListTheEndpointsThatADAGMayUse(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, in := range []aiendpoint.CreateInput{
		globalInput("b-global", keyOne, []string{"*"}, []string{"*"}),
		globalInput("a-listed", keyOne, []string{"*"}, []string{"fix-bug"}),
		globalInput("c-unlisted", keyOne, []string{"*"}, []string{"review"}),
		globalInput("d-hidden", keyOne, []string{"*"}, []string{"*"}),
		workspaceInput("d-hidden", keyTwo, "team", []string{"*"}),
		workspaceInput("e-team", keyTwo, "team", []string{"*"}),
		workspaceInput("f-other", keyTwo, "other", []string{"*"}),
	} {
		_, err := f.svc.Create(ctx, in)
		require.NoError(t, err, in.Name)
	}

	names := func(workspace string) []string {
		choices, err := f.svc.Choices(ctx, "fix-bug", workspace)
		require.NoError(t, err)
		var out []string
		for _, c := range choices {
			out = append(out, c.Name+"@"+c.Scope.String())
		}
		return out
	}
	assert.Equal(t, []string{"a-listed@global", "b-global@global", "d-hidden@global"}, names(""))
	assert.Equal(t, []string{"a-listed@global", "b-global@global", "d-hidden@workspace:team", "e-team@workspace:team"}, names("team"))
}

func TestReadKeyReasons(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	created, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	// A secret that an administrator deleted with the secret routes gives key_missing.
	require.NoError(t, f.secrets.Delete(ctx, created.SecretID))
	_, err = f.svc.ReadKey(ctx, *created)
	assert.Equal(t, aiendpoint.ReasonKeyMissing, aiendpoint.ReasonOf(err))

	// A disabled secret gives unavailable.
	other, err := f.svc.Create(ctx, globalInput("cle2", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	sec, err := f.secrets.GetByID(ctx, other.SecretID)
	require.NoError(t, err)
	require.NoError(t, sec.SetStatus(secret.StatusDisabled, "admin", sec.UpdatedAt))
	require.NoError(t, f.secrets.Update(ctx, sec))
	_, err = f.svc.ReadKey(ctx, *other)
	assert.Equal(t, aiendpoint.ReasonUnavailable, aiendpoint.ReasonOf(err))
	assert.NotContains(t, err.Error(), keyOne)
}

func TestCheckProtocol(t *testing.T) {
	t.Parallel()

	anthropic := aiendpoint.Endpoint{Name: "a", Protocol: aiendpoint.ProtocolAnthropic}
	openai := aiendpoint.Endpoint{Name: "o", Protocol: aiendpoint.ProtocolOpenAI}
	require.NoError(t, anthropic.CheckProtocol("claude"))
	assert.Equal(t, aiendpoint.ReasonProtocolMismatch, aiendpoint.ReasonOf(openai.CheckProtocol("claude")))
	assert.Equal(t, aiendpoint.ReasonProtocolMismatch, aiendpoint.ReasonOf(anthropic.CheckProtocol("codex")))
}
