// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"fmt"
	"slices"
)

// Lookup reads the endpoint of a name in one scope. It returns nil and no error
// when the scope has no such endpoint.
type Lookup func(scope Scope) (*Endpoint, error)

// Resolve applies the rules of Spec 093, "Scope and name" and "Use rule". It is a
// pure function: lookup is the only access to the store.
//
// A DAG with a workspace resolves the name in the scope of the workspace first,
// and then in the global scope. A workspace endpoint hides the global endpoint of
// the same name. The DAG may use the endpoint that it finds, or Resolve returns
// the reason endpoint_forbidden. A DAG with no workspace resolves in the global
// scope only. A name that no visible scope has gives the reason endpoint_unknown.
func Resolve(name, dagName, workspaceName string, lookup Lookup) (*Endpoint, error) {
	found, err := visibleIn(workspaceName, lookup)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, &Error{
			Reason: ReasonUnknown,
			Name:   name,
			Detail: "no endpoint of this name is visible to the DAG",
		}
	}
	if err := found.CheckUse(dagName, workspaceName); err != nil {
		return nil, err
	}
	return found, nil
}

// visibleIn finds the entry that a DAG with a workspace sees: the entry of the
// workspace first, and then the entry of the global scope. A DAG with no
// workspace sees the global scope only. It returns nil when no scope has it.
func visibleIn[T any](workspaceName string, lookup func(Scope) (*T, error)) (*T, error) {
	if workspaceName != "" {
		found, err := lookup(WorkspaceScope(workspaceName))
		if err != nil || found != nil {
			return found, err
		}
	}
	return lookup(GlobalScope())
}

// CheckUse applies the use rule of Spec 093. A DAG may use an endpoint when all
// these conditions are true:
//
//   - the endpoint is global, or it is in the workspace of the DAG
//   - the workspace of the DAG is in allow.workspaces, for a global endpoint
//   - the name of the DAG is in allow.dags
//
// The value * matches each name, and a DAG with no workspace. It returns the
// reason endpoint_forbidden when a condition is false.
func (e *Endpoint) CheckUse(dagName, workspaceName string) error {
	return checkUse("endpoint", ReasonForbidden, e.Name, e.Scope, e.Allow, dagName, workspaceName)
}

// checkUse applies the use rule to an entry of a scope. kind names the entry in the
// error text.
func checkUse(kind string, reason Reason, name string, scope Scope, allow Allow, dagName, workspaceName string) error {
	forbidden := func(format string, args ...any) error {
		return &Error{Reason: reason, Name: name, Kind: kind, Detail: fmt.Sprintf(format, args...)}
	}
	if scope.IsGlobal() {
		if !allowed(allow.Workspaces, workspaceName) {
			if workspaceName == "" {
				return forbidden("the DAG %q has no workspace, and the allow list of workspaces has no *", dagName)
			}
			return forbidden("the workspace %q of the DAG %q is not in the allow list", workspaceName, dagName)
		}
	} else if scope.Workspace != workspaceName {
		return forbidden("the %s is in the workspace %q", kind, scope.Workspace)
	}
	if !allowed(allow.DAGs, dagName) {
		return forbidden("the DAG %q is not in the allow list", dagName)
	}
	return nil
}

// allowed tells if an allow list matches a name. An empty name matches the
// value * only.
func allowed(list []string, name string) bool {
	if slices.Contains(list, Wildcard) {
		return true
	}
	return name != "" && slices.Contains(list, name)
}

// CheckProtocol checks that the client can talk to the endpoint. The CLI
// provider claude needs the protocol anthropic. It returns the reason
// endpoint_protocol_mismatch otherwise.
func (e *Endpoint) CheckProtocol(provider string) error {
	var need Protocol
	switch provider {
	case "claude":
		need = ProtocolAnthropic
	default:
		return &Error{Reason: ReasonProtocolMismatch, Name: e.Name,
			Detail: fmt.Sprintf("the provider %q has no protocol that an endpoint can serve", provider)}
	}
	if e.Protocol != need {
		return &Error{Reason: ReasonProtocolMismatch, Name: e.Name,
			Detail: fmt.Sprintf("the provider %q needs the protocol %s, and the endpoint has %s", provider, need, e.Protocol)}
	}
	return nil
}

// CheckKey checks that the endpoint has a key, or that its preset accepts no
// key. It reads no key value. It returns the reason endpoint_key_missing
// otherwise.
func (e *Endpoint) CheckKey() error {
	if e.HasKey() {
		return nil
	}
	if row, ok := LookupPreset(e.Preset, e.Protocol); ok && row.AllowsNoKey() {
		return nil
	}
	return &Error{Reason: ReasonKeyMissing, Name: e.Name, Detail: "the endpoint has no key"}
}
