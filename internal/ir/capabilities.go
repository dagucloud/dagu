// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"slices"
	"strconv"
)

// Spec 094, "Version check". A worker announces the capabilities that it can
// run. A DAG needs a capability when one of its steps uses a feature that only a
// new worker serves. The coordinator matches a task only with a worker that
// announces each required capability.

// CapabilityAIRelay is the capability of a worker that can run the AI relay.
const CapabilityAIRelay = "ai_relay.v1"

// WorkerTooOld is the reason of a run that no worker with the capability can take.
const WorkerTooOld = "worker_too_old"

// AIRelayStep is a step that names an AI endpoint or a MCP server entry. Endpoint
// is empty for a step that names a MCP server only.
type AIRelayStep struct {
	Step      string
	Endpoint  string
	MCPServer string
}

// RequiredCapabilities returns the capabilities that a worker must announce to
// run the DAG: the capability of the AI relay for a step that names an
// endpoint or a MCP server entry, in the steps, in the handler steps and in the
// body of a foreach step. An
// agent.run step names an endpoint with with.endpoint.name. A chat.completion step
// names one with with.endpoint or with the endpoint of a model entry. Both name a
// MCP server entry with an entry of tools.mcp that has no url. A step of a
// sub-DAG does not count.
func (d *DAG) RequiredCapabilities() []string {
	if len(d.AIRelaySteps()) == 0 {
		return nil
	}
	return []string{CapabilityAIRelay}
}

// AIRelaySteps returns each step that names an AI endpoint or a MCP server entry,
// in the order of the DAG.
func (d *DAG) AIRelaySteps() []AIRelayStep {
	if d == nil {
		return nil
	}
	var steps []AIRelayStep
	d.WalkSteps(func(step *Step) {
		endpoints, servers := step.AIEndpoints(), step.AIMCPServers()
		if len(endpoints) == 0 && len(servers) == 0 {
			return
		}
		relayStep := AIRelayStep{Step: step.Name}
		if len(endpoints) > 0 {
			relayStep.Endpoint = endpoints[0]
		}
		if len(servers) > 0 {
			relayStep.MCPServer = servers[0]
		}
		steps = append(steps, relayStep)
	})
	return steps
}

// WorkerHasCapabilities reports whether the worker announces each required
// capability.
func WorkerHasCapabilities(have, need []string) bool {
	for _, capability := range need {
		if !slices.Contains(have, capability) {
			return false
		}
	}
	return true
}

// DescribeMissingAIRelay returns the error text of a run that no worker with the
// capability can take. It names the first step that needs the relay and its
// endpoint, or its MCP server entry. It returns false when the DAG needs no such
// capability.
func (d *DAG) DescribeMissingAIRelay() (string, bool) {
	steps := d.AIRelaySteps()
	if len(steps) == 0 {
		return "", false
	}
	first := steps[0]
	if first.Endpoint == "" {
		return fmt.Sprintf("no worker supports the AI relay: the step %s names the MCP server %s (%s)",
			strconv.Quote(first.Step), strconv.Quote(first.MCPServer), WorkerTooOld), true
	}
	return fmt.Sprintf("no worker supports the AI relay: the step %s names the endpoint %s (%s)",
		strconv.Quote(first.Step), strconv.Quote(first.Endpoint), WorkerTooOld), true
}
