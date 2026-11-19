// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
)

// provider is a fake models API. It answers with the current status and body.
type provider struct {
	mu     sync.Mutex
	status int
	body   string
	calls  atomic.Int32
	keys   []string
	// hook runs at the start of each request, when it is set.
	hook   func()
	server *httptest.Server
}

func newProvider(t *testing.T, body string) *provider {
	t.Helper()
	p := &provider{status: http.StatusOK, body: body}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.calls.Add(1)
		p.mu.Lock()
		hook := p.hook
		p.mu.Unlock()
		if hook != nil {
			hook()
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.keys = append(p.keys, r.Header.Get("Authorization"))
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *provider) set(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body = status, body
}

type modelsFixture struct {
	*fixture
	provider *provider
	models   *store.AIEndpointModelsStore
	clock    *fakeClock
}

// fakeClock is a clock that a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newModelsFixture(t *testing.T) *modelsFixture {
	t.Helper()
	f := newFixture(t)
	p := newProvider(t, `{"data":[{"id":"m-1"},{"id":"m-2"}]}`)
	models, err := store.NewAIEndpointModelsStore(f.backend.Collection("ai_endpoint_models"))
	require.NoError(t, err)
	reader, err := aiendpoint.NewModelsReader(p.server.Client(), 2*time.Second)
	require.NoError(t, err)
	require.NoError(t, f.svc.EnableModels(models, reader))
	clock := &fakeClock{now: time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)}
	f.svc.SetClock(clock.Now)
	return &modelsFixture{fixture: f, provider: p, models: models, clock: clock}
}

func (f *modelsFixture) create(t *testing.T, name string) *aiendpoint.Endpoint {
	t.Helper()
	ep, err := f.svc.Create(context.Background(), aiendpoint.CreateInput{
		Name:      name,
		Preset:    aiendpoint.PresetCustom,
		Protocol:  aiendpoint.ProtocolOpenAI,
		BaseURL:   f.provider.server.URL,
		Auth:      aiendpoint.AuthBearer,
		ModelsURL: f.provider.server.URL + "/v1/models",
		Scope:     aiendpoint.GlobalScope(),
		Allow:     aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}},
		Key:       modelsKey,
		Actor:     "admin",
	})
	require.NoError(t, err)
	return ep
}

func TestRefreshModelsKeepsTheListWithItsDate(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")

	got, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"m-1", "m-2"}, got.Names)
	assert.Equal(t, f.clock.Now(), got.RefreshedAt)
	assert.Empty(t, got.LastError)
	assert.Equal(t, []string{"Bearer " + modelsKey}, f.provider.keys, "the call uses the key of the secret store")

	ep, err := f.svc.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	stored, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Equal(t, got.Names, stored.Names)
}

func TestFailedRefreshKeepsTheLastGoodListAndRecordsTheError(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	good, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)

	f.clock.Advance(time.Hour)
	f.provider.set(http.StatusInternalServerError, "boom "+modelsKey)
	got, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err, "the error of the provider is a result, not an error of the call")
	assert.Equal(t, good.Names, got.Names)
	assert.Equal(t, good.RefreshedAt, got.RefreshedAt, "the date of the last good read stays")
	assert.Contains(t, got.LastError, "status 500")
	assert.NotContains(t, got.LastError, modelsKey)
	assert.Equal(t, f.clock.Now(), got.LastErrorAt)
	assert.Equal(t, f.clock.Now(), got.AttemptedAt)

	// A good read clears the error.
	f.clock.Advance(time.Hour)
	f.provider.set(http.StatusOK, `{"data":[{"id":"m-3"}]}`)
	got, err = f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"m-3"}, got.Names)
	assert.Empty(t, got.LastError)
	assert.True(t, got.LastErrorAt.IsZero())
}

func TestFirstFailedReadHasNoListAndAnError(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	f.provider.set(http.StatusForbidden, "")

	got, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Empty(t, got.Names)
	assert.True(t, got.RefreshedAt.IsZero())
	assert.Contains(t, got.LastError, "status 403")
}

func TestRefreshModelsWithoutURLOrEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)

	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "missing")
	assert.ErrorIs(t, err, aiendpoint.ErrNotFound)

	// Z.ai has no models URL in the table.
	_, err = f.svc.Create(ctx, aiendpoint.CreateInput{
		Name: "zai1", Preset: "zai", Protocol: aiendpoint.ProtocolOpenAI, Scope: aiendpoint.GlobalScope(),
		Allow: aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}}, Key: modelsKey, Actor: "admin",
	})
	require.NoError(t, err)
	_, err = f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "zai1")
	assert.ErrorIs(t, err, aiendpoint.ErrNoModelsURL)
	assert.Zero(t, f.provider.calls.Load())
}

func TestRefreshModelsWithoutAKeyRecordsTheError(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	// An endpoint of a preset that needs a key has one. A secret that was removed gives the error.
	ep := f.create(t, "cle1")
	require.NoError(t, f.secrets.Delete(ctx, ep.SecretID))

	got, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Contains(t, got.LastError, "endpoint_key_missing")
	assert.Zero(t, f.provider.calls.Load())
}

func TestDeleteRemovesTheListAndCreateStartsClean(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)

	require.NoError(t, f.svc.Delete(ctx, aiendpoint.GlobalScope(), "cle1"))
	_, err = f.models.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	assert.ErrorIs(t, err, aiendpoint.ErrNotFound)

	// A list that a failed delete left behind does not show for a new endpoint.
	require.NoError(t, f.models.Put(ctx, &aiendpoint.Models{Scope: aiendpoint.GlobalScope(), Name: "cle1", Names: []string{"old"}}))
	ep := f.create(t, "cle1")
	got, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Empty(t, got.Names)
}

func TestRefreshModelsDoesNotSaveAListForADeletedEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	// The provider deletes the endpoint while the read runs.
	f.provider.mu.Lock()
	f.provider.hook = func() { _ = f.svc.Delete(ctx, aiendpoint.GlobalScope(), "cle1") }
	f.provider.mu.Unlock()

	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	assert.ErrorIs(t, err, aiendpoint.ErrNotFound)
	all, err := f.models.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

// An endpoint deleted and created again with the same name while the read runs
// does not get the list of the old endpoint.
func TestRefreshModelsDoesNotSaveAListForARecreatedEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	var once sync.Once
	f.provider.mu.Lock()
	f.provider.hook = func() {
		once.Do(func() {
			require.NoError(t, f.svc.Delete(ctx, aiendpoint.GlobalScope(), "cle1"))
			f.create(t, "cle1")
		})
	}
	f.provider.mu.Unlock()

	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.Error(t, err)
	all, err := f.models.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, all, "the list of the old endpoint is not saved for the new one")
}

func TestRefreshModelsAsyncReadsInTheBackground(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	ep := f.create(t, "cle1")

	f.svc.RefreshModelsAsync(ctx, ep.Scope, ep.Name)
	f.svc.WaitModels()

	got, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Equal(t, []string{"m-1", "m-2"}, got.Names)
}

func TestModelsListsAreNotInTheEndpointRecord(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)

	// A change of the endpoint keeps the list.
	desc := "changed"
	_, err = f.svc.Update(ctx, aiendpoint.GlobalScope(), "cle1", aiendpoint.UpdateInput{Description: &desc, Actor: "admin"})
	require.NoError(t, err)
	ep, err := f.svc.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	got, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Len(t, got.Names, 2)
}

func TestModelsStoreNeverHoldsTheKey(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.create(t, "cle1")
	f.provider.set(http.StatusUnauthorized, "bad key "+modelsKey)
	_, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)

	page, err := f.backend.Collection("ai_endpoint_models").List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	for _, rec := range page.Records {
		assert.NotContains(t, string(rec.Data), modelsKey)
	}
}

// A read of the protocol anthropic keeps the details. A failed read keeps them
// with the last good list.
func TestRefreshModelsKeepsTheDetailsOfTheProtocolAnthropic(t *testing.T) {
	ctx := context.Background()
	f := newModelsFixture(t)
	f.provider.set(http.StatusOK, string(fixtureBytes(t, "anthropic_details.json")))
	_, err := f.svc.Create(ctx, aiendpoint.CreateInput{
		Name:      "claude1",
		Preset:    aiendpoint.PresetCustom,
		Protocol:  aiendpoint.ProtocolAnthropic,
		BaseURL:   f.provider.server.URL,
		Auth:      aiendpoint.AuthXAPIKey,
		ModelsURL: f.provider.server.URL + "/v1/models",
		Scope:     aiendpoint.GlobalScope(),
		Allow:     aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}},
		Key:       modelsKey,
		Actor:     "admin",
	})
	require.NoError(t, err)

	got, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "claude1")
	require.NoError(t, err)
	require.Len(t, got.Info, 3)
	assert.Equal(t, "Claude Opus 5.5", got.Info[0].DisplayName)
	assert.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, got.Info[0].Efforts)

	f.provider.set(http.StatusInternalServerError, "boom")
	again, err := f.svc.RefreshModels(ctx, aiendpoint.GlobalScope(), "claude1")
	require.NoError(t, err)
	assert.Equal(t, got.Info, again.Info)
}

func TestServiceWithoutModelsHasNoList(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ep, err := f.svc.Create(ctx, globalInput("cle1", keyOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	assert.False(t, f.svc.ModelsEnabled())
	got, err := f.svc.ModelsOf(ctx, ep)
	require.NoError(t, err)
	assert.Empty(t, got.Names)
	_, err = f.svc.RefreshModels(ctx, ep.Scope, ep.Name)
	assert.ErrorIs(t, err, aiendpoint.ErrModelsDisabled)
}
