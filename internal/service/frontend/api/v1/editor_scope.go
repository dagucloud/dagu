// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// envNamePattern is the env name rule of specs/003-value-resolution.md.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// buildReferenceScope lists the values that steps of dag can reference.
// Every array is non-nil. It returns nil when dag is nil.
func buildReferenceScope(dag *ir.DAG) *api.ReferenceScope {
	if dag == nil {
		return nil
	}
	return &api.ReferenceScope{
		Params:  scopeParams(dag),
		Consts:  scopeConsts(dag),
		Env:     scopeEnv(dag),
		Secrets: scopeSecrets(dag),
		Steps:   scopeSteps(dag),
	}
}

// scopeParams lists the named params of dag in definition order.
func scopeParams(dag *ir.DAG) []api.ReferenceScopeVariable {
	params := []api.ReferenceScopeVariable{}
	for _, def := range dag.ParamDefs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		v := api.ReferenceScopeVariable{Name: name, Reference: "${params." + name + "}"}
		if def.Description != "" {
			v.Description = ptrOf(def.Description)
		}
		if def.Default != nil {
			v.DefaultValue = new(fmt.Sprint(def.Default))
		}
		params = append(params, v)
	}
	return params
}

// scopeConsts lists the consts of dag sorted by name. Only scalar values
// (string, bool, integer and float kinds) give a default value.
func scopeConsts(dag *ir.DAG) []api.ReferenceScopeVariable {
	names := make([]string, 0, len(dag.Consts))
	for name := range dag.Consts {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	consts := make([]api.ReferenceScopeVariable, 0, len(names))
	for _, name := range names {
		v := api.ReferenceScopeVariable{Name: name, Reference: "${consts." + name + "}"}
		if text, ok := scalarText(dag.Consts[name]); ok {
			// An empty string const keeps its empty default; ptrOf would drop it.
			v.DefaultValue = &text
		}
		consts = append(consts, v)
	}
	return consts
}

// scalarText renders value as text when it is a string, bool, integer or
// float kind. Maps, lists, nil and other kinds give false.
func scalarText(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	//nolint:exhaustive // Only scalar kinds give a text.
	switch reflect.ValueOf(value).Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(value), true
	default:
		return "", false
	}
}

// scopeEnv lists the names of the env entries of dag, deduplicated and sorted.
// It never reads the value of an entry: the response must not carry values,
// which can hold secrets.
func scopeEnv(dag *ir.DAG) []api.ReferenceScopeVariable {
	seen := make(map[string]struct{}, len(dag.Env))
	names := make([]string, 0, len(dag.Env))
	for _, entry := range dag.Env {
		name, _, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !found || !envNamePattern.MatchString(name) {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)

	env := make([]api.ReferenceScopeVariable, 0, len(names))
	for _, name := range names {
		env = append(env, api.ReferenceScopeVariable{Name: name, Reference: "${env." + name + "}"})
	}
	return env
}

// scopeSecrets lists the names of the secrets of dag, deduplicated and sorted.
// A secret reaches steps as an environment variable of its name. The scope
// never carries the value, the key, the registry ref or the options. The
// description names the provider only.
func scopeSecrets(dag *ir.DAG) []api.ReferenceScopeVariable {
	byName := make(map[string]api.ReferenceScopeVariable, len(dag.Secrets))
	names := make([]string, 0, len(dag.Secrets))
	for _, secret := range dag.Secrets {
		name := strings.TrimSpace(secret.Name)
		if !envNamePattern.MatchString(name) {
			continue
		}
		if _, dup := byName[name]; dup {
			continue
		}
		v := api.ReferenceScopeVariable{Name: name, Reference: "${env." + name + "}"}
		if provider := strings.TrimSpace(secret.Provider); provider != "" {
			v.Description = ptrOf("Secret from provider " + provider)
		}
		byName[name] = v
		names = append(names, name)
	}
	sort.Strings(names)

	secrets := make([]api.ReferenceScopeVariable, 0, len(names))
	for _, name := range names {
		secrets = append(secrets, byName[name])
	}
	return secrets
}
