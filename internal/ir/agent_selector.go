// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Spec 092, "Routing". A DAG that needs a CLI provider has the implied label
// agent.provider.<provider>: "true". Only an agent worker of that provider
// announces the label, and an agent worker matches only a task that has it.

const (
	// AgentLabelPrefix is the prefix of the labels that Dagu sets for agent
	// workers. A worker_selector, a run selector and worker.labels cannot use it.
	AgentLabelPrefix = "agent."
	// AgentProviderLabelPrefix is the prefix of the label of the provider of an
	// agent worker.
	AgentProviderLabelPrefix = AgentLabelPrefix + "provider."
	// AgentProviderLabelValue is the value of the label of the provider.
	AgentProviderLabelValue = "true"
)

// IsAgentLabel reports whether the label key belongs to Dagu for agent workers.
func IsAgentLabel(key string) bool {
	return strings.HasPrefix(key, AgentLabelPrefix)
}

// AgentProviderLabel returns the label key of the provider.
func AgentProviderLabel(provider string) string {
	return AgentProviderLabelPrefix + provider
}

// IsAgentCLIProvider reports whether the name is a CLI provider of Spec 091: a
// built-in CLI harness provider that is not also a chat provider. A name of both
// kinds is a chat provider for agent.run, so no agent worker serves it.
func IsAgentCLIProvider(name string) bool {
	if !IsBuiltinCLIHarnessProvider(name) {
		return false
	}
	_, err := llm.ParseProviderType(name)
	return err != nil
}

// AgentCLIProviderNames returns the CLI providers of Spec 091, sorted.
func AgentCLIProviderNames() []string {
	var names []string
	for _, name := range BuiltinCLIHarnessProviderNames() {
		if IsAgentCLIProvider(name) {
			names = append(names, name)
		}
	}
	return names
}

// AgentProviders returns the CLI providers that the DAG needs, sorted and
// without duplicates: the provider of each agent.run step, in the steps, in the
// handler steps and in the body of a foreach step. A step of a sub-DAG does not
// count.
func (d *DAG) AgentProviders() []string {
	if d == nil {
		return nil
	}
	seen := map[string]struct{}{}
	d.WalkSteps(func(step *Step) {
		if step.Agent != nil && step.Agent.Provider != "" {
			seen[step.Agent.Provider] = struct{}{}
		}
	})
	if len(seen) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(seen))
}

// DispatchWorkerSelector returns the selector that a task for this DAG carries: the
// worker_selector of the DAG and the implied selector of its CLI providers (Spec
// 092, "Routing"). It returns the worker_selector itself when the DAG needs no
// provider.
func (d *DAG) DispatchWorkerSelector() map[string]string {
	if d == nil {
		return nil
	}
	return WithAgentWorkerSelector(d.WorkerSelector, d)
}

// AgentWorkerSelector returns the implied selector of the DAG: the label
// agent.provider.<provider> with the value "true" for each provider that the DAG
// needs. The result is nil when the DAG needs no provider.
func (d *DAG) AgentWorkerSelector() map[string]string {
	providers := d.AgentProviders()
	if len(providers) == 0 {
		return nil
	}
	selector := make(map[string]string, len(providers))
	for _, provider := range providers {
		selector[AgentProviderLabel(provider)] = AgentProviderLabelValue
	}
	return selector
}

// WithAgentWorkerSelector returns the selector with the implied selector of the
// DAG added. It returns the selector itself when the DAG needs no provider.
func WithAgentWorkerSelector(selector map[string]string, dag *DAG) map[string]string {
	implied := dag.AgentWorkerSelector()
	if len(implied) == 0 {
		return selector
	}
	merged := make(map[string]string, len(selector)+len(implied))
	maps.Copy(merged, selector)
	maps.Copy(merged, implied)
	return merged
}

// WithoutAgentLabels returns the selector without the labels that start with
// agent. It returns the selector itself when it has no such label.
func WithoutAgentLabels(selector map[string]string) map[string]string {
	found := false
	for key := range selector {
		if IsAgentLabel(key) {
			found = true
			break
		}
	}
	if !found {
		return selector
	}
	out := make(map[string]string, len(selector))
	for key, value := range selector {
		if !IsAgentLabel(key) {
			out[key] = value
		}
	}
	return out
}

// RoutedWorkerSelector returns the selector of a task for the DAG: the labels
// that start with agent. come from the DAG only. The function removes such
// labels of the caller and adds the implied selector of the DAG.
func RoutedWorkerSelector(selector map[string]string, dag *DAG) map[string]string {
	return WithAgentWorkerSelector(WithoutAgentLabels(selector), dag)
}

// SelectorAgentProviders returns the providers that the selector asks for, sorted.
func SelectorAgentProviders(selector map[string]string) []string {
	var providers []string
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		if provider, ok := strings.CutPrefix(key, AgentProviderLabelPrefix); ok {
			providers = append(providers, provider)
		}
	}
	return providers
}

// WorkerLabelsMatchSelector is the match rule of Spec 092. Each label of the
// selector must have the same value on the worker. A worker with a label
// agent.provider.<P> matches only a selector that has that label too. The
// coordinator, the dispatch store, the scheduler and the worker use this rule.
func WorkerLabelsMatchSelector(workerLabels, selector map[string]string) bool {
	for key, value := range selector {
		if workerValue, ok := workerLabels[key]; !ok || workerValue != value {
			return false
		}
	}
	for key := range workerLabels {
		if !strings.HasPrefix(key, AgentProviderLabelPrefix) {
			continue
		}
		if _, ok := selector[key]; !ok {
			return false
		}
	}
	return true
}

// CheckReservedSelectorKey returns an error when the key of a worker_selector or
// of a run selector belongs to Dagu.
func CheckReservedSelectorKey(key string) error {
	switch {
	case IsAgentLabel(key):
		return fmt.Errorf("worker selector %q: the keys that start with %s belong to Dagu, which sets them from the agent.run steps of the DAG",
			key, AgentLabelPrefix)
	}
	return nil
}

// FormatQuotedWorkerSelector renders a selector as {key: "value", ...} with
// sorted keys.
func FormatQuotedWorkerSelector(selector map[string]string) string {
	parts := make([]string, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		parts = append(parts, key+": "+strconv.Quote(selector[key]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// DescribeAgentSelector names the provider of a selector that needs an agent
// worker: `no agent worker serves the provider "claude": the selector is
// {agent.provider.claude: "true"}`. It returns false when the selector names no
// provider.
func DescribeAgentSelector(selector map[string]string) (string, bool) {
	providers := SelectorAgentProviders(selector)
	if len(providers) == 0 {
		return "", false
	}
	return fmt.Sprintf("no agent worker serves the %s %s: the selector is %s",
		pluralWord(len(providers), "provider", "providers"), quotedList(providers), FormatQuotedWorkerSelector(selector)), true
}

// TwoProvidersError is the validation error of a DAG that needs more than one
// provider.
func TwoProvidersError(providers []string) error {
	return fmt.Errorf("the DAG needs the providers %s: a run goes to one agent worker", quotedList(providers))
}

// quotedList renders "a", "b" and "c".
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = strconv.Quote(item)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
