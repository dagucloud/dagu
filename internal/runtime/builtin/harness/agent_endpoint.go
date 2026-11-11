// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// This file is the part of the agent.run step that uses the model gateway of
// Spec 094. A step with the key endpoint checks its endpoint when it starts,
// starts a relay for each attempt, and runs the CLI with the address of the relay
// and the relay secret. The CLI never sees the key of the provider.

// reasonRelayUnavailable is the reason of a step that has no gateway to use.
const reasonRelayUnavailable = string(aigateway.ReasonRelayUnavailable)

// stepRef returns the step as the gateway names it.
func (a *agentHarness) stepRef(ctx context.Context) aigateway.StepRef {
	return agentstep.GatewayStep(ctx, a.step.Name)
}

// checkTargets is the start check of Spec 094, "Start checks": the endpoint of the
// step, and then each named MCP server. It runs before the CLI starts and before
// any request goes to a provider or a MCP server. A failed check fails the step
// with the exit code 1, the kind error and the reason of the failure.
func (a *agentHarness) checkTargets(ctx context.Context) error {
	if endpoint := a.step.Agent.Endpoint; endpoint != "" {
		result, err := a.checkTarget(ctx, aigateway.EndpointTarget(endpoint), a.step.Agent.Provider, a.resolvedModel())
		if err != nil {
			return err
		}
		a.relayProtocol = result.Protocol
	}
	for _, name := range a.step.AIMCPServers() {
		if _, err := a.checkTarget(ctx, aigateway.MCPTarget(name), "", ""); err != nil {
			return err
		}
	}
	return nil
}

// resolvedModel returns the model of the step after resolution. It is empty when
// the step sets none, which means the default model of the CLI.
func (a *agentHarness) resolvedModel() string {
	if len(a.configs) == 0 {
		return ""
	}
	model, _ := a.configs[0].flags["model"].(string)
	return model
}

// checkTarget calls the start check of the gateway for one target. A model that is
// not empty also passes the model rule of Spec 093, "Use in a step".
func (a *agentHarness) checkTarget(ctx context.Context, target aigateway.Target, provider, model string) (aigateway.CheckResult, error) {
	client := aigateway.ClientFrom(ctx)
	if client == nil {
		return aigateway.CheckResult{}, executor.NewAgentError(ir.AgentKindError, 1, reasonRelayUnavailable,
			fmt.Errorf("step %q: %s: %s: this process has no AI gateway", a.step.Name, target, reasonRelayUnavailable))
	}
	result, err := aigateway.CheckStart(ctx, client, a.stepRef(ctx), target, provider, model)
	if err != nil {
		failure := aigateway.FailureOf(err)
		return aigateway.CheckResult{}, executor.NewAgentError(ir.AgentKindError, 1, string(failure.Reason),
			fmt.Errorf("step %q: %s: %s", a.step.Name, target, failure))
	}
	return result, nil
}

// startRelay starts the relay of one attempt. It serves the endpoint of the step,
// if any, and the named MCP servers of the step.
func (a *agentHarness) startRelay(ctx context.Context) (*aigateway.Relay, error) {
	return aigateway.StartRelay(ctx, aigateway.RelayOptions{
		Client:     aigateway.ClientFrom(ctx),
		Step:       a.stepRef(ctx),
		Endpoint:   a.step.Agent.Endpoint,
		Protocol:   a.relayProtocol,
		MCPServers: a.step.AIMCPServers(),
		Log:        a.stderrWriter(),
	})
}

// relayEnvironment returns the environment of the CLI for an attempt with a
// relay: the base URL of the relay, the relay secret as the placeholder key, and
// NO_PROXY with 127.0.0.1 and localhost added, so that a proxy of the deployment
// never gets a request for the relay. It drops each variable of the environment
// that can give the CLI another route or another credential: the names that
// start with ANTHROPIC_ or CLAUDE_CODE_USE_, and CLAUDE_CODE_OAUTH_TOKEN. The
// step gets no model route of the egress proxy.
func relayEnvironment(env []string, baseURL, secret string) []string {
	out := make([]string, 0, len(env)+4)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") || key == "CLAUDE_CODE_OAUTH_TOKEN" {
			continue
		}
		out = append(out, entry)
	}
	out = loopbackNoProxy(out)
	return append(out,
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY="+secret,
	)
}

// loopbackNoProxy adds 127.0.0.1 and localhost to NO_PROXY and no_proxy, so that a
// proxy of the deployment never gets a request for the relay. A step that names
// MCP servers only needs this and no model variable.
func loopbackNoProxy(env []string) []string {
	var noProxy []string
	out := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if key != "NO_PROXY" && key != "no_proxy" {
			out = append(out, entry)
			continue
		}
		for item := range strings.SplitSeq(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				noProxy = append(noProxy, item)
			}
		}
	}
	for _, local := range []string{"127.0.0.1", "localhost"} {
		if !slices.Contains(noProxy, local) {
			noProxy = append(noProxy, local)
		}
	}
	list := strings.Join(noProxy, ",")
	return append(out, "NO_PROXY="+list, "no_proxy="+list)
}

// relayRefusal returns the last refusal of the relay, or nil when the attempt has
// no relay or the relay refused nothing.
func relayRefusal(relay *aigateway.Relay) *aigateway.Failure {
	if relay == nil {
		return nil
	}
	return relay.LastRefusal()
}
