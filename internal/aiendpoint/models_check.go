// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ModelRefreshInterval is the shortest time between two on-demand refreshes of the
// models list of one endpoint (Spec 093, "Use in a step").
const ModelRefreshInterval = 60 * time.Second

// CLIModelProvider is the CLI provider whose model aliases the check accepts.
const CLIModelProvider = "claude"

// CLIModelAliases are the model names that the CLI claude accepts and that the
// models API of the provider does not list. The set comes from the Claude Code
// version 2.1.311. This is the only place of the set. A name of the set never
// fails the model check. The names are lower case. The suffix
// CLIModelLongContextSuffix is also valid on each name.
var CLIModelAliases = []string{
	"default", "best", "sonnet", "opus", "haiku", "fable", "mythos", "opusplan",
}

// CLIModelLongContextSuffix is the suffix of a name of the CLI that asks for the
// context window of 1M tokens, for example `sonnet[1m]`.
const CLIModelLongContextSuffix = "[1m]"

// IsCLIModelAlias tells if the CLI claude accepts the name with no entry in the
// models list: an alias, or an alias with the suffix `[1m]`.
func IsCLIModelAlias(name string) bool {
	base, _ := strings.CutSuffix(name, CLIModelLongContextSuffix)
	return slices.Contains(CLIModelAliases, strings.ToLower(base))
}

// CheckModel applies the model rule of Spec 093, "Use in a step". The step names
// the endpoint ep and a model that is a literal string after resolution. The model
// MUST be in the models list of the endpoint when the endpoint has a list that a
// read made. It returns the reason model_unknown otherwise.
//
// The check is skipped, and it returns nil, in these cases: the model is empty,
// the provider is the CLI claude and the model is an alias of the CLI, the
// service keeps no lists, the endpoint has no models URL, the list was never read,
// or the list is empty. A failed last read keeps the last good list, so the check
// uses it.
//
// On a miss, CheckModel refreshes the list once, and checks again. It refreshes
// at most once in ModelRefreshInterval for an endpoint, so a wrong model cannot
// make a call to the provider for each step. The refresh ends with the models
// timeout. Only the second miss fails.
func (s *Service) CheckModel(ctx context.Context, ep Endpoint, provider, model string) error {
	if model == "" || s.models == nil {
		return nil
	}
	cli := provider == CLIModelProvider
	if cli && IsCLIModelAlias(model) {
		return nil
	}
	list, err := s.ModelsOf(ctx, &ep)
	if err != nil {
		// A store that fails cannot tell the list: the provider answers at the first request.
		return nil
	}
	if listed(list, model, cli) {
		return nil
	}
	if s.mayRefreshOnDemand(ep) {
		if fresh, err := s.RefreshModels(ctx, ep.Scope, ep.Name); err == nil && fresh != nil {
			list = fresh
		}
		if listed(list, model, cli) {
			return nil
		}
	}
	return &Error{Reason: ReasonModelUnknown, Name: ep.Name, Detail: fmt.Sprintf(
		"the model %q is not in the models list of the endpoint (read at %s); reload the list on the AI page, or fix the model",
		model, list.RefreshedAt.UTC().Format(time.RFC3339))}
}

// listed tells if the model passes the list. It is true when the list cannot reject
// a model: no read made it, or it has no name. The CLI also accepts a listed
// model with the suffix `[1m]`.
func listed(list *Models, model string, cli bool) bool {
	if list == nil || list.RefreshedAt.IsZero() || len(list.Names) == 0 {
		return true
	}
	if slices.Contains(list.Names, model) {
		return true
	}
	base, found := strings.CutSuffix(model, CLIModelLongContextSuffix)
	return cli && found && slices.Contains(list.Names, base)
}

// mayRefreshOnDemand tells if an on-demand refresh of the endpoint may run now,
// and records the time of this refresh.
func (s *Service) mayRefreshOnDemand(ep Endpoint) bool {
	key := ep.Scope.String() + "/" + ep.Name
	now := s.now()
	s.models.refreshMu.Lock()
	defer s.models.refreshMu.Unlock()
	if last, ok := s.models.onDemand[key]; ok && now.Sub(last) < ModelRefreshInterval {
		return false
	}
	if s.models.onDemand == nil {
		s.models.onDemand = map[string]time.Time{}
	}
	s.models.onDemand[key] = now
	return true
}
