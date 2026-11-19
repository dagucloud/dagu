// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// This file is the part of the chat.completion step that uses the model gateway
// of Spec 094. A model with an endpoint is checked when the step starts. Each
// attempt of the model then starts a relay, and the provider client calls the
// relay with the relay secret. The step never holds the key of the endpoint.

// endpointRoute is what the start check says about one endpoint.
type endpointRoute struct {
	protocol aiendpoint.Protocol
	chatType llmpkg.ProviderType
}

// checkEndpoints is the start check of Spec 094, "Start checks". It runs before
// the first request of the step, for each endpoint of the models. A failed check
// fails the step at once, with the reason of the failure. No request goes to a
// provider.
func (e *Executor) checkEndpoints(ctx context.Context, models []ir.ModelEntry) (map[string]endpointRoute, error) {
	routes := make(map[string]endpointRoute)
	type checked struct{ endpoint, model string }
	done := make(map[checked]bool)
	for _, model := range models {
		name := model.Endpoint
		if name == "" || done[checked{name, model.Name}] {
			continue
		}
		done[checked{name, model.Name}] = true
		client := aigateway.ClientFrom(ctx)
		if client == nil {
			return nil, e.endpointError(name, aigateway.Fail(aigateway.ReasonRelayUnavailable, "this process has no AI gateway"))
		}
		// The provider type comes from the endpoint, so no provider is checked. The
		// model of each entry passes the model rule of Spec 093, "Use in a step".
		result, err := aigateway.CheckStart(ctx, client, agentstep.GatewayStep(ctx, e.step.Name), aigateway.EndpointTarget(name), "", model.Name)
		if err != nil {
			return nil, e.endpointError(name, aigateway.FailureOf(err))
		}
		if _, ok := routes[name]; ok {
			continue
		}
		route, err := routeOf(result)
		if err != nil {
			return nil, e.endpointError(name, aigateway.Fail(aigateway.ReasonEndpointProtocolMismatch, "%s", err.Error()))
		}
		routes[name] = route
	}
	return routes, nil
}

// routeOf reads the protocol and the chat type of a start check. An endpoint of a
// preset that gives no chat type has the type of its protocol.
func routeOf(result aigateway.CheckResult) (endpointRoute, error) {
	chatType := result.ChatType
	if chatType == "" {
		chatType = string(result.Protocol)
	}
	providerType, err := llmpkg.ParseProviderType(chatType)
	if err != nil {
		return endpointRoute{}, fmt.Errorf("the chat type %q of the endpoint is not a provider type", chatType)
	}
	return endpointRoute{protocol: result.Protocol, chatType: providerType}, nil
}

func (e *Executor) endpointError(endpoint string, failure *aigateway.Failure) error {
	return fmt.Errorf("step %q: endpoint %q: %s", e.step.Name, endpoint, failure)
}

// relayBaseURL returns the base URL that the provider client calls: the address
// of the relay, and the prefix /v1 for the protocol openai.
func relayBaseURL(relay *aigateway.Relay, protocol aiendpoint.Protocol) string {
	if protocol == aiendpoint.ProtocolOpenAI {
		return relay.URL() + "/v1"
	}
	return relay.URL()
}

// startRelay starts the relay of one attempt of one model, and builds the provider
// client that calls it. The caller closes the relay when the attempt ends.
func (e *Executor) startRelay(ctx context.Context, endpoint string, route endpointRoute) (llmpkg.Provider, *aigateway.Relay, error) {
	relay, err := aigateway.StartRelay(ctx, aigateway.RelayOptions{
		Client:   aigateway.ClientFrom(ctx),
		Step:     agentstep.GatewayStep(ctx, e.step.Name),
		Endpoint: endpoint,
		Protocol: route.protocol,
		Log:      e.stderr,
	})
	if err != nil {
		return nil, nil, e.endpointError(endpoint, aigateway.Fail(aigateway.ReasonRelayUnavailable, "%s", err.Error()))
	}
	provider, err := e.newRelayProvider(route.chatType, relayBaseURL(relay, route.protocol), relay.Secret())
	if err != nil {
		_ = relay.Close()
		return nil, nil, err
	}
	return provider, relay, nil
}

// withRefusal adds the reason of the last refusal of the relay to the error of an
// attempt, so that the step reports the reason of Spec 094, "Failure kinds".
func withRefusal(err error, endpoint string, relay *aigateway.Relay) error {
	if err == nil || relay == nil {
		return err
	}
	if refusal := relay.LastRefusal(); refusal != nil {
		return fmt.Errorf("%w; endpoint %q: the AI relay refused a request: %s", err, endpoint, refusal)
	}
	return err
}

// startMCPRelay is the start check and the relay of the named MCP servers of the
// step (Spec 095, "The MCP relay"). Each name is checked first. The relay then
// serves the MCP client of the step for the whole step. The caller closes it when
// the step ends. The step never holds a credential of a MCP server entry.
func startMCPRelay(ctx context.Context, step ir.Step, log io.Writer) (*aigateway.Relay, error) {
	names := step.AIMCPServers()
	client := aigateway.ClientFrom(ctx)
	ref := agentstep.GatewayStep(ctx, step.Name)
	fail := func(name string, failure *aigateway.Failure) error {
		return fmt.Errorf("step %q: MCP server %q: %s", step.Name, name, failure)
	}
	for _, name := range names {
		if client == nil {
			return nil, fail(name, aigateway.Fail(aigateway.ReasonRelayUnavailable, "this process has no AI gateway"))
		}
		if _, err := client.Check(ctx, ref, aigateway.MCPTarget(name), ""); err != nil {
			return nil, fail(name, aigateway.FailureOf(err))
		}
	}
	relay, err := aigateway.StartRelay(ctx, aigateway.RelayOptions{
		Client:     client,
		Step:       ref,
		MCPServers: names,
		Log:        log,
	})
	if err != nil {
		return nil, fail(names[0], aigateway.Fail(aigateway.ReasonRelayUnavailable, "%s", err.Error()))
	}
	return relay, nil
}
