// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// checkFixture is an endpoint whose list was read once: m-1 and m-2.
func newCheckFixture(t *testing.T) (*modelsFixture, aiendpoint.Endpoint) {
	t.Helper()
	f := newModelsFixture(t)
	ep := f.create(t, "cle1")
	f.svc.WaitModels()
	_, err := f.svc.RefreshModels(context.Background(), ep.Scope, ep.Name)
	require.NoError(t, err)
	f.provider.calls.Store(0)
	return f, *ep
}

func TestCheckModelListed(t *testing.T) {
	f, ep := newCheckFixture(t)
	require.NoError(t, f.svc.CheckModel(context.Background(), ep, "", "m-2"))
	assert.Zero(t, f.provider.calls.Load(), "a listed model makes no call to the provider")
}

func TestCheckModelUnlistedFailsWithTheReason(t *testing.T) {
	f, ep := newCheckFixture(t)
	err := f.svc.CheckModel(context.Background(), ep, "", "ghost")
	require.Error(t, err)
	assert.Equal(t, aiendpoint.ReasonModelUnknown, aiendpoint.ReasonOf(err))
	assert.Equal(t, `endpoint "cle1": model_unknown: the model "ghost" is not in the models list of the endpoint `+
		`(read at 2026-11-04T08:00:00Z); reload the list on the AI page, or fix the model`, err.Error())
	assert.EqualValues(t, 1, f.provider.calls.Load(), "a miss refreshes the list once")
}

// A new model that the provider released after the last read passes after the
// on-demand refresh.
func TestCheckModelRefreshThenFound(t *testing.T) {
	f, ep := newCheckFixture(t)
	f.provider.set(http.StatusOK, `{"data":[{"id":"m-1"},{"id":"m-2"},{"id":"m-3"}]}`)
	require.NoError(t, f.svc.CheckModel(context.Background(), ep, "", "m-3"))
	assert.EqualValues(t, 1, f.provider.calls.Load())

	stored, err := f.svc.ModelsOf(context.Background(), &ep)
	require.NoError(t, err)
	assert.Equal(t, []string{"m-1", "m-2", "m-3"}, stored.Names, "the refresh keeps the new list")
}

func TestCheckModelRefreshThenStillMissingNamesTheNewDate(t *testing.T) {
	f, ep := newCheckFixture(t)
	f.clock.Advance(time.Hour)
	err := f.svc.CheckModel(context.Background(), ep, "", "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read at 2026-11-04T09:00:00Z", "the date is the date of the refresh")
}

// At most one on-demand refresh for an endpoint in 60 seconds.
func TestCheckModelRefreshIsRateLimited(t *testing.T) {
	f, ep := newCheckFixture(t)
	ctx := context.Background()
	require.Error(t, f.svc.CheckModel(ctx, ep, "", "ghost"))
	assert.EqualValues(t, 1, f.provider.calls.Load())

	f.clock.Advance(aiendpoint.ModelRefreshInterval - time.Second)
	f.provider.set(http.StatusOK, `{"data":[{"id":"m-1"},{"id":"ghost"}]}`)
	require.Error(t, f.svc.CheckModel(ctx, ep, "", "ghost"), "the second miss in the window gives no refresh")
	assert.EqualValues(t, 1, f.provider.calls.Load())

	f.clock.Advance(time.Second)
	require.NoError(t, f.svc.CheckModel(ctx, ep, "", "ghost"), "the window ended: the refresh runs and finds the model")
	assert.EqualValues(t, 2, f.provider.calls.Load())
}

func TestCheckModelSkippedCases(t *testing.T) {
	ctx := context.Background()

	t.Run("empty model", func(t *testing.T) {
		f, ep := newCheckFixture(t)
		require.NoError(t, f.svc.CheckModel(ctx, ep, "claude", ""))
		assert.Zero(t, f.provider.calls.Load())
	})

	t.Run("list never read", func(t *testing.T) {
		f := newModelsFixture(t)
		f.provider.set(http.StatusForbidden, "")
		ep := f.create(t, "cle1")
		f.svc.WaitModels()
		f.provider.calls.Store(0)
		stored, err := f.svc.ModelsOf(ctx, ep)
		require.NoError(t, err)
		require.True(t, stored.RefreshedAt.IsZero())
		// The refresh on a miss fails too, so the step goes on.
		require.NoError(t, f.svc.CheckModel(ctx, *ep, "", "anything"))
	})

	t.Run("empty list", func(t *testing.T) {
		f := newModelsFixture(t)
		f.provider.set(http.StatusOK, `{"data":[]}`)
		ep := f.create(t, "cle1")
		f.svc.WaitModels()
		require.NoError(t, f.svc.CheckModel(ctx, *ep, "", "anything"))
	})

	t.Run("no models URL", func(t *testing.T) {
		f := newModelsFixture(t)
		ep, err := f.svc.Create(ctx, aiendpoint.CreateInput{
			Name: "zai1", Preset: "zai", Protocol: aiendpoint.ProtocolOpenAI, Scope: aiendpoint.GlobalScope(),
			Allow: aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}}, Key: modelsKey, Actor: "admin",
		})
		require.NoError(t, err)
		require.NoError(t, f.svc.CheckModel(ctx, *ep, "", "anything"))
		assert.Zero(t, f.provider.calls.Load())
	})
}

// A failed last read keeps the last good list, and the check uses it.
func TestCheckModelUsesTheLastGoodListAfterAFailedRead(t *testing.T) {
	f, ep := newCheckFixture(t)
	ctx := context.Background()
	f.clock.Advance(time.Hour)
	f.provider.set(http.StatusInternalServerError, "")
	_, err := f.svc.RefreshModels(ctx, ep.Scope, ep.Name)
	require.NoError(t, err)

	require.NoError(t, f.svc.CheckModel(ctx, ep, "", "m-1"))
	err = f.svc.CheckModel(ctx, ep, "", "ghost")
	require.Error(t, err)
	assert.Equal(t, aiendpoint.ReasonModelUnknown, aiendpoint.ReasonOf(err))
	assert.Contains(t, err.Error(), "read at 2026-11-04T08:00:00Z", "the date is the date of the last good read")
}

func TestCheckModelServiceWithoutListsSkipsTheCheck(t *testing.T) {
	f := newFixture(t)
	ep := aiendpoint.Endpoint{Name: "cle1", Scope: aiendpoint.GlobalScope()}
	require.NoError(t, f.svc.CheckModel(context.Background(), ep, "", "anything"))
}

// The CLI claude accepts aliases that the models API does not list.
func TestCheckModelAliasesOfTheCLI(t *testing.T) {
	f, ep := newCheckFixture(t)
	ctx := context.Background()
	for _, alias := range []string{"sonnet", "opus", "haiku", "opusplan", "default", "best", "sonnet[1m]", "opus[1m]", "Sonnet"} {
		require.NoError(t, f.svc.CheckModel(ctx, ep, "claude", alias), alias)
	}
	assert.Zero(t, f.provider.calls.Load(), "an alias makes no call")

	// A listed model with the suffix passes, and an unlisted one fails.
	require.NoError(t, f.svc.CheckModel(ctx, ep, "claude", "m-1[1m]"))
	assert.Error(t, f.svc.CheckModel(ctx, ep, "claude", "ghost[1m]"))
}

// A chat step calls the API with the name, so an alias is a name like another.
func TestCheckModelAliasIsNotAcceptedForAChatStep(t *testing.T) {
	f, ep := newCheckFixture(t)
	err := f.svc.CheckModel(context.Background(), ep, "", "sonnet")
	require.Error(t, err)
	assert.Equal(t, aiendpoint.ReasonModelUnknown, aiendpoint.ReasonOf(err))
}

func TestIsCLIModelAlias(t *testing.T) {
	for _, name := range []string{"sonnet", "OPUS", "opusplan", "default", "best", "haiku[1m]", "fable"} {
		assert.True(t, aiendpoint.IsCLIModelAlias(name), name)
	}
	for _, name := range []string{"", "[1m]", "claude-sonnet-4-6", "sonnet-4", "gpt-5"} {
		assert.False(t, aiendpoint.IsCLIModelAlias(name), name)
	}
}
