// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agenttools"
)

// Spec 091, "Tool bridge": what an attempt with the provider claude builds
// around the process for the Dagu tools of the step.

// reasonBridgeUnavailable is the reason of an attempt that fails because the
// CLI cannot reach the tool bridge.
const reasonBridgeUnavailable = "bridge_unavailable"

// bridgeConnectedStatus is the status that the event system/init gives to a
// server that the CLI reached (to confirm in Spec 091).
const bridgeConnectedStatus = "connected"

// isBridgeEntry reports whether the tool bridge serves the entry: a DAG tool.
// The mcp and builtin entries go to the CLI.
func isBridgeEntry(entry ir.ToolEntry) bool {
	return entry.Kind == ir.ToolEntryDAG
}

// buildBridgeRegistry builds the effective catalog of the Dagu tools of the
// step, as Spec 090 defines it. It returns nil when the step has no Dagu tool.
// The limits max_tools and tool_result_max_bytes come from the Dagu configuration,
// as for chat.completion.
func buildBridgeRegistry(ctx context.Context, cli *ir.AgentCLIConfig) (*agenttools.ToolRegistry, error) {
	if cli == nil {
		return nil, nil
	}
	hasBridgeEntry := slices.ContainsFunc(cli.Tools, isBridgeEntry)
	if !hasBridgeEntry {
		return nil, nil
	}
	registry, err := agenttools.BuildToolRegistryWith(ctx, &ir.LLMConfig{
		ToolEntries: cli.Tools,
	}, agenttools.BuildOptions{
		DAGsPath:                "with.dags",
		ToolsPath:               "with.tools",
		Skip:                    func(entry ir.ToolEntry) bool { return !isBridgeEntry(entry) },
		RefuseSubstitutedInputs: true,
	})
	if err != nil {
		return nil, err
	}
	if err := checkBridgeToolNames(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

// maxCLIToolNameLen is the longest name that the CLI accepts for a tool. The
// CLI names a tool of the bridge mcp__dagu__<name>.
const maxCLIToolNameLen = 64

// checkBridgeToolNames fails when the CLI name of a tool of the bridge is too
// long. Dagu does not cut the name: the author uses a DAG or an action with a
// shorter name.
func checkBridgeToolNames(registry *agenttools.ToolRegistry) error {
	prefix := "mcp__" + agenttools.BridgeServerName + "__"
	for _, name := range registry.Names() {
		if len(prefix+name) > maxCLIToolNameLen {
			return fmt.Errorf("the tool %q is too long for the tool bridge: the CLI limit for %s<name> is %d characters; use a shorter name",
				name, prefix, maxCLIToolNameLen)
		}
	}
	return nil
}

// startBridge starts the tool bridge of an attempt. The catalog of the step is
// the same for each attempt, and each attempt has its own bridge, port and
// secret.
func (a *agentHarness) startBridge(ctx context.Context, input *agentInput) (*agenttools.Bridge, error) {
	return agenttools.StartBridge(ctx, agenttools.BridgeOptions{
		Registry:       input.registry,
		WorkDir:        runtime.GetEnv(ctx).WorkingDir,
		ResultMaxBytes: agenttools.ToolResultMaxBytes(ctx, nil),
		MaxToolCalls:   a.step.Agent.EffectiveMaxToolCalls(),
		DAGRunID:       runctx.GetContext(ctx).DAGRunID,
		Step:           a.step.Name,
	})
}

// bridgeServer returns the entry of the bridge for the MCP config. The header
// carries the secret of the attempt, so the value goes to the config file and
// nowhere else.
func bridgeServer(bridge *agenttools.Bridge) claudeMCPServer {
	return claudeMCPServer{
		name:    agenttools.BridgeServerName,
		url:     bridge.URL(),
		headers: map[string]string{"Authorization": "Bearer " + bridge.Secret()},
		allow:   bridge.ToolNames(),
	}
}

// withRelayServers gives each named MCP server its address on the relay of the
// attempt. The header carries the relay secret, so the value goes to the config
// file and nowhere else. The credentials of the server entry never reach the CLI.
// The other servers stay as they are.
func withRelayServers(servers []claudeMCPServer, relay *aigateway.Relay) []claudeMCPServer {
	out := make([]claudeMCPServer, len(servers))
	for i, server := range servers {
		if server.named {
			server.url = relay.URL() + aigateway.MCPPathPrefix + server.name
			server.headers = map[string]string{"Authorization": "Bearer " + relay.Secret()}
		}
		out[i] = server
	}
	return out
}

// bridgeReached reports whether the CLI connected to the bridge, as the event
// system/init says. A stream with no such event gives no answer: the failure
// has another cause, which the classes of Spec 091 report.
func bridgeReached(stream *claudeStream) (answered, reached bool) {
	if stream.mcpServers == nil {
		return false, false
	}
	return true, stream.mcpServers[agenttools.BridgeServerName] == bridgeConnectedStatus
}

// streamSecrets returns the values that the raw stream of the CLI must not hold:
// the secrets of the attempt and the secrets of the run. A tool of the agent can
// print a secret of the run, and the stream is a JSON text, so each value is
// also given in its JSON form.
func streamSecrets(ctx context.Context, attempt []string) []string {
	values := slices.Clone(attempt)
	for _, value := range runtime.SecretsForMasking(ctx) {
		values = append(values, value)
		if data, err := json.Marshal(value); err == nil {
			if escaped := string(data[1 : len(data)-1]); escaped != value {
				values = append(values, escaped)
			}
		}
	}
	return values
}

// secretMasker masks the secrets of an attempt in the text that goes to a file
// or a log. It returns nil when there is no secret.
func secretMasker(secrets []string) *masking.Masker {
	if len(secrets) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		pairs = append(pairs, "SECRET="+secret)
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: pairs})
}
