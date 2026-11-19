// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
)

// modelSource is an endpoint source that also checks the model.
type modelSource struct {
	*fakeSource
	calls []string
	err   error
}

func (m *modelSource) CheckModel(_ context.Context, _ aiendpoint.Endpoint, provider, model string) error {
	m.calls = append(m.calls, provider+"/"+model)
	return m.err
}

// The model check runs after the checks of the endpoint, only for a model that is
// not empty, and its reason reaches the caller.
func TestCheckModel(t *testing.T) {
	ep := endpointFor("https://api.anthropic.com", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey)
	ep.Preset = "anthropic"
	subject := aigateway.Subject{DAGName: "fix-bug"}
	ctx := context.Background()

	src := &modelSource{fakeSource: &fakeSource{ep: ep}}
	g, err := aigateway.New(aigateway.Config{Endpoints: src, Limits: testLimits()})
	require.NoError(t, err)

	_, failure := g.CheckModel(ctx, subject, aigateway.EndpointTarget("cle1"), "claude", "")
	require.Nil(t, failure)
	assert.Empty(t, src.calls, "an empty model makes no model check")

	_, failure = g.CheckModel(ctx, subject, aigateway.EndpointTarget("cle1"), "claude", "sonnet")
	require.Nil(t, failure)
	assert.Equal(t, []string{"claude/sonnet"}, src.calls)

	src.err = &aiendpoint.Error{Reason: aiendpoint.ReasonModelUnknown, Name: "cle1", Detail: "the model is not listed"}
	_, failure = g.CheckModel(ctx, subject, aigateway.EndpointTarget("cle1"), "claude", "ghost")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonModelUnknown, failure.Reason)
	assert.True(t, failure.Reason.Known(), "the reason is in the table, so the worker can read it")

	_, failure = g.CheckModel(ctx, subject, aigateway.EndpointTarget("other"), "claude", "ghost")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointUnknown, failure.Reason, "an unknown endpoint fails before the model check")

	// Check keeps its meaning: no model.
	src.calls = nil
	_, failure = g.Check(ctx, subject, aigateway.EndpointTarget("cle1"), "claude")
	require.Nil(t, failure)
	assert.Empty(t, src.calls)
}

// A source with no model check lets the step go on.
func TestCheckModelWithASourceThatHasNoModelCheck(t *testing.T) {
	ep := endpointFor("https://api.anthropic.com", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey)
	g := newGateway(t, &fakeSource{ep: ep}, testLimits(), nil)
	_, failure := g.CheckModel(context.Background(), aigateway.Subject{DAGName: "fix-bug"}, aigateway.EndpointTarget("cle1"), "claude", "ghost")
	assert.Nil(t, failure)
}
