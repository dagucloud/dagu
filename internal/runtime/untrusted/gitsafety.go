// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// Spec 103, "Protections for later trusted steps". In a run with an untrusted
// step, git in a trusted process opens a bare repository only when the command
// names it (safe.bareRepository=explicit). So a folder that an agent makes to
// look like a repository does not give its config to git, for example
// core.fsmonitor. The setting goes through the variables GIT_CONFIG_COUNT,
// GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n>, after the entries that the
// variables already have. Git reads GIT_CONFIG_PARAMETERS after them, so the
// setting also goes at its end when it is set.

const (
	gitSafetyKey   = "safe.bareRepository"
	gitSafetyValue = "explicit"
	// gitSafetyParameter is the setting as an entry of GIT_CONFIG_PARAMETERS.
	gitSafetyParameter = "'" + gitSafetyKey + "=" + gitSafetyValue + "'"
)

// GitSafetyEntries returns the variables to set so that git applies
// safe.bareRepository=explicit, given the variables that lookup reads. It
// returns nil when the variables already apply it.
func GitSafetyEntries(lookup func(string) (string, bool)) map[string]string {
	entries := countedGitSafety(lookup)
	params, _ := lookup("GIT_CONFIG_PARAMETERS")
	if params = strings.TrimSpace(params); params != "" && !endsWithGitSafety(params) {
		if entries == nil {
			entries = make(map[string]string, 1)
		}
		entries["GIT_CONFIG_PARAMETERS"] = params + " " + gitSafetyParameter
	}
	return entries
}

// countedGitSafety returns the counted entries to add, or nil when the last
// counted entry of the key applies the setting.
func countedGitSafety(lookup func(string) (string, bool)) map[string]string {
	count := 0
	if raw, ok := lookup("GIT_CONFIG_COUNT"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && n > 0 {
			count = n
		}
	}
	// Git applies the last entry of a key. It refuses a count with a missing
	// entry, so the entries after a missing one never apply.
	applied := false
	for i := range count {
		key, ok := lookup("GIT_CONFIG_KEY_" + strconv.Itoa(i))
		if !ok {
			break
		}
		value, ok := lookup("GIT_CONFIG_VALUE_" + strconv.Itoa(i))
		if !ok {
			break
		}
		if strings.EqualFold(strings.TrimSpace(key), gitSafetyKey) {
			applied = strings.EqualFold(strings.TrimSpace(value), gitSafetyValue)
		}
	}
	if applied {
		return nil
	}
	index := strconv.Itoa(count)
	return map[string]string{
		"GIT_CONFIG_COUNT":          strconv.Itoa(count + 1),
		"GIT_CONFIG_KEY_" + index:   gitSafetyKey,
		"GIT_CONFIG_VALUE_" + index: gitSafetyValue,
	}
}

// endsWithGitSafety reports whether the last entry of the quoted list params
// is gitSafetyParameter.
func endsWithGitSafety(params string) bool {
	rest, ok := strings.CutSuffix(params, gitSafetyParameter)
	return ok && (rest == "" || strings.HasSuffix(rest, " "))
}

// withGitSafety returns env with the variables of GitSafetyEntries.
func withGitSafety(env []string) []string {
	lookup := func(name string) (string, bool) {
		for _, e := range slices.Backward(env) {
			key, value, ok := strings.Cut(e, "=")
			if ok && key == name {
				return value, true
			}
		}
		return "", false
	}
	entries := GitSafetyEntries(lookup)
	if len(entries) == 0 {
		return env
	}
	for key, value := range entries {
		env = setEnv(env, key, value)
	}
	return env
}

type gitSafetyKeyType struct{}

// WithGitSafetyContext marks ctx as the context of a trusted step of a run with
// an untrusted step: StartManagedProcess adds the variables of withGitSafety.
func WithGitSafetyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, gitSafetyKeyType{}, true)
}

func gitSafetyFrom(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	on, _ := ctx.Value(gitSafetyKeyType{}).(bool)
	return on
}
