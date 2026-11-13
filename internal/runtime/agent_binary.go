// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import "context"

// Spec 092, "The agent worker". The option --agent <provider>=<path> names the
// binary of the provider. The worker puts it in the context of each run, so the
// step of the provider runs that binary and not a binary that PATH finds.

type agentProviderBinaryKey struct{}

type agentProviderBinary struct {
	provider string
	path     string
}

// WithAgentProviderBinary returns a context in which the built-in CLI provider
// runs the binary at path.
func WithAgentProviderBinary(ctx context.Context, provider, path string) context.Context {
	return context.WithValue(ctx, agentProviderBinaryKey{}, agentProviderBinary{provider: provider, path: path})
}

// AgentProviderBinary returns the binary of the provider that the context names.
// It returns false for another provider or for a context with no binary.
func AgentProviderBinary(ctx context.Context, provider string) (string, bool) {
	binary, ok := ctx.Value(agentProviderBinaryKey{}).(agentProviderBinary)
	if !ok || binary.provider != provider || binary.path == "" {
		return "", false
	}
	return binary.path, true
}
