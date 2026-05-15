// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
)

// CheckCapability describes whether a provider can check access without
// materializing plaintext secret values.
type CheckCapability string

const (
	CheckCapabilityNoFetch           CheckCapability = "no_fetch"
	CheckCapabilityMetadataOnly      CheckCapability = "metadata_only"
	CheckCapabilityRequiresValueRead CheckCapability = "requires_value_read"
	CheckCapabilityUnsupported       CheckCapability = "unsupported"
)

// CheckCapabilityError is returned when the default check path cannot run
// without violating the no-value-read contract.
type CheckCapabilityError struct {
	Provider   string
	Capability CheckCapability
}

func (e *CheckCapabilityError) Error() string {
	switch e.Capability {
	case CheckCapabilityRequiresValueRead:
		return fmt.Sprintf("secret provider %q requires reading secret values for access checks", e.Provider)
	case CheckCapabilityUnsupported:
		return fmt.Sprintf("secret provider %q does not support access checks", e.Provider)
	default:
		return fmt.Sprintf("secret provider %q cannot perform access checks with capability %q", e.Provider, e.Capability)
	}
}

// Resolver fetches secret values from a specific backend.
// Implementations must be thread-safe as they may be called concurrently.
type Resolver interface {
	// Name returns the provider identifier (e.g., "env", "file", "vault").
	Name() string

	// Resolve fetches the secret value for the given reference.
	// Returns an error if the secret cannot be retrieved.
	Resolve(ctx context.Context, ref core.SecretRef) (string, error)

	// Validate checks if the secret reference is structurally valid for this provider.
	// This is called at parse time and should not make network calls.
	Validate(ref core.SecretRef) error

	// CheckCapability reports whether CheckAccessibility can run without
	// fetching plaintext secret values.
	CheckCapability(ref core.SecretRef) CheckCapability

	// CheckAccessibility verifies the secret is accessible. Providers may use
	// plaintext reads only when CheckCapability reports RequiresValueRead.
	// Callers that must avoid plaintext reads must check CheckCapability first.
	// Should verify:
	//   - Provider is reachable
	//   - Credentials are valid
	//   - Secret exists
	//   - Caller has permission
	CheckAccessibility(ctx context.Context, ref core.SecretRef) error
}

// ReferenceResolver resolves workspace-local team secret registry references.
// Implementations are responsible for authorization, provenance, and avoiding
// plaintext persistence according to their deployment mode.
type ReferenceResolver interface {
	ResolveReference(ctx context.Context, ref core.SecretRef) (string, error)
	CheckReferenceAccessibility(ctx context.Context, ref core.SecretRef) error
}

// Registry manages all secret resolvers.
// It is thread-safe and can be used concurrently.
type Registry struct {
	resolvers         map[string]Resolver
	mu                sync.RWMutex
	baseDirs          []string
	referenceResolver ReferenceResolver
}

var (
	// globalResolvers stores resolver factories that are registered via init()
	globalResolvers = make(map[string]func(baseDirs []string) Resolver)
	globalMu        sync.RWMutex
)

// registerResolver adds a resolver factory to be used by all registries.
// This is called from init() functions in resolver implementation files.
func registerResolver(name string, factory func(baseDirs []string) Resolver) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalResolvers[name] = factory
}

// NewRegistry creates a new registry with all registered providers.
// baseDirs is used by the file provider to resolve relative paths.
// The file provider tries each base directory in order until a file is found.
func NewRegistry(baseDirs ...string) *Registry {
	return newRegistry(nil, baseDirs...)
}

// NewRegistryWithReferenceResolver creates a registry that can resolve
// workspace-local team secret references through the provided resolver.
func NewRegistryWithReferenceResolver(referenceResolver ReferenceResolver, baseDirs ...string) *Registry {
	return newRegistry(referenceResolver, baseDirs...)
}

func newRegistry(referenceResolver ReferenceResolver, baseDirs ...string) *Registry {
	globalMu.RLock()
	defer globalMu.RUnlock()

	r := &Registry{
		resolvers:         make(map[string]Resolver),
		baseDirs:          baseDirs,
		referenceResolver: referenceResolver,
	}

	// Instantiate all registered providers
	for name, factory := range globalResolvers {
		r.resolvers[name] = factory(baseDirs)
	}

	return r
}

// Register adds a custom resolver to the registry.
// If a resolver with the same name already exists, it will be replaced.
// This is useful for adding custom providers or testing.
func (r *Registry) Register(name string, res Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers[name] = res
}

// Get retrieves a resolver by provider name.
// Returns nil if the provider is not registered.
func (r *Registry) Get(provider string) Resolver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolvers[provider]
}

// Resolve fetches a single secret value.
// Returns an error if the provider is unknown or resolution fails.
func (r *Registry) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	if ref.Ref != "" {
		if ref.Provider != "" || ref.Key != "" || len(ref.Options) > 0 {
			return "", fmt.Errorf("secret %q registry ref cannot include provider, key, or options", ref.Name)
		}
		if r.referenceResolver == nil {
			return "", fmt.Errorf("secret registry resolver is not configured for secret %q ref %q", ref.Name, ref.Ref)
		}
		value, err := r.referenceResolver.ResolveReference(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("failed to resolve secret %q from registry ref %q: %w", ref.Name, ref.Ref, err)
		}
		return value, nil
	}

	if ref.Provider == "" {
		return "", fmt.Errorf("provider is required for secret %q", ref.Name)
	}

	res := r.Get(ref.Provider)
	if res == nil {
		return "", fmt.Errorf("unknown secret provider: %s", ref.Provider)
	}

	if err := res.Validate(ref); err != nil {
		return "", fmt.Errorf("invalid secret reference for %q: %w", ref.Name, err)
	}

	value, err := res.Resolve(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("failed to resolve secret %q from provider %q: %w", ref.Name, ref.Provider, err)
	}

	return value, nil
}

// ResolveAll fetches all secrets and returns them as environment variable strings.
// Format: "NAME=value"
// Returns an error if any secret fails to resolve.
func (r *Registry) ResolveAll(ctx context.Context, refs []core.SecretRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	envVars := make([]string, 0, len(refs))

	for _, ref := range refs {
		value, err := r.Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		envVars = append(envVars, fmt.Sprintf("%s=%s", ref.Name, value))
	}

	return envVars, nil
}

// CheckAccessibility validates that all secrets are accessible through no-fetch
// or metadata-only provider checks. Providers that require value reads are
// rejected with CheckCapabilityError instead of being called.
func (r *Registry) CheckAccessibility(ctx context.Context, refs []core.SecretRef) error {
	if len(refs) == 0 {
		return nil
	}

	for _, ref := range refs {
		if ref.Ref != "" {
			if ref.Provider != "" || ref.Key != "" || len(ref.Options) > 0 {
				return fmt.Errorf("secret %q registry ref cannot include provider, key, or options", ref.Name)
			}
			if r.referenceResolver == nil {
				return fmt.Errorf("secret registry resolver is not configured for secret %q ref %q", ref.Name, ref.Ref)
			}
			if err := r.referenceResolver.CheckReferenceAccessibility(ctx, ref); err != nil {
				return fmt.Errorf("secret %q registry ref %q is not accessible: %w", ref.Name, ref.Ref, err)
			}
			continue
		}

		if ref.Provider == "" {
			return fmt.Errorf("provider is required for secret %q", ref.Name)
		}

		res := r.Get(ref.Provider)
		if res == nil {
			return fmt.Errorf("unknown secret provider: %s", ref.Provider)
		}

		if err := res.Validate(ref); err != nil {
			return fmt.Errorf("invalid secret reference for %q: %w", ref.Name, err)
		}

		switch capability := res.CheckCapability(ref); capability {
		case CheckCapabilityNoFetch, CheckCapabilityMetadataOnly:
		case CheckCapabilityRequiresValueRead, CheckCapabilityUnsupported:
			return &CheckCapabilityError{
				Provider:   ref.Provider,
				Capability: capability,
			}
		default:
			return fmt.Errorf("secret provider %q reported unknown check capability %q", ref.Provider, capability)
		}

		if err := res.CheckAccessibility(ctx, ref); err != nil {
			return fmt.Errorf("secret %q is not accessible: %w", ref.Name, err)
		}
	}

	return nil
}

// Providers returns the names of all registered providers.
func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.resolvers))
	for name := range r.resolvers {
		names = append(names, name)
	}
	return names
}
