// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// Spec 092, "Trusted steps after an untrusted step". In a run with an untrusted
// step, git in a trusted process opens a bare repository only when the command
// names it (safe.bareRepository=explicit). So a folder that an agent makes to
// look like a repository does not give its config to git, for example
// core.fsmonitor. The setting goes through the variables GIT_CONFIG_COUNT,
// GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n>, after the entries that the
// variables already have.

const (
	gitSafetyKey   = "safe.bareRepository"
	gitSafetyValue = "explicit"
)

// GitSafetyEntries returns the variables to add so that git applies
// safe.bareRepository=explicit, given the variables that lookup reads. It
// returns nil when the variables already apply it.
func GitSafetyEntries(lookup func(string) (string, bool)) map[string]string {
	count := 0
	if raw, ok := lookup("GIT_CONFIG_COUNT"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && n > 0 {
			count = n
		}
	}
	for i := range count {
		key, _ := lookup("GIT_CONFIG_KEY_" + strconv.Itoa(i))
		value, _ := lookup("GIT_CONFIG_VALUE_" + strconv.Itoa(i))
		if strings.EqualFold(strings.TrimSpace(key), gitSafetyKey) && strings.EqualFold(strings.TrimSpace(value), gitSafetyValue) {
			return nil
		}
	}
	index := strconv.Itoa(count)
	return map[string]string{
		"GIT_CONFIG_COUNT":          strconv.Itoa(count + 1),
		"GIT_CONFIG_KEY_" + index:   gitSafetyKey,
		"GIT_CONFIG_VALUE_" + index: gitSafetyValue,
	}
}

// WithGitSafety returns env with the variables of GitSafetyEntries.
func WithGitSafety(env []string) []string {
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
// an untrusted step: StartManagedProcess adds the variables of WithGitSafety.
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
