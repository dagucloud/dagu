// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/hashicorp/vault/api"
)

func init() {
	registerResolver("vault", func(_ []string) Resolver {
		return &vaultResolver{}
	})
}

// vaultResolver fetches secrets from HashiCorp Vault.
// It supports both KV v1 and v2 engines and can use a convention-based
// approach where the last segment of the key is treated as the field name.
type vaultResolver struct {
	client vaultClient // For testing
	// clientFactory is used to create real clients in tests without contacting Vault.
	clientFactory func(vaultClientSettings) (vaultClient, error)
	mu            sync.Mutex

	// Caching real clients
	cachedClient   vaultClient
	cachedSettings vaultClientSettings
}

type vaultClientSettings struct {
	address string
	token   string
}

// Name returns the provider identifier.
func (r *vaultResolver) Name() string {
	return "vault"
}

// Validate checks if the secret reference is valid for Vault.
func (r *vaultResolver) Validate(ref core.SecretRef) error {
	if strings.TrimSpace(ref.Key) == "" {
		return fmt.Errorf("key (vault secret path) is required")
	}
	return nil
}

// CheckCapability reports that the current Vault check implementation must
// read the secret to verify field-level access.
func (r *vaultResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

// Resolve fetches the secret value from HashiCorp Vault.
func (r *vaultResolver) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	client, err := r.getClient(ctx, ref)
	if err != nil {
		return "", err
	}

	vaultPath, field := r.parseKey(ref)

	data, err := client.Read(ctx, vaultPath)
	if err != nil {
		return "", fmt.Errorf("vault read error: %w", err)
	}

	if data == nil {
		msg := fmt.Sprintf("secret not found at path %q", vaultPath)
		if !strings.Contains(vaultPath, "/data/") {
			msg += " (Note: for KV v2 engines, path must include '/data/')"
		}
		return "", fmt.Errorf("%s", msg)
	}

	// Handle KV v2 engine response unwrapping
	if val, ok := data["data"]; ok {
		if nestedData, ok := val.(map[string]any); ok {
			data = nestedData
		}
	}

	val, ok := data[field]
	if !ok {
		available := getKeys(data)
		return "", fmt.Errorf("field %q not found in vault secret %q (available fields: %s)", field, vaultPath, strings.Join(available, ", "))
	}

	slog.Debug("successfully resolved vault secret", "name", ref.Name, "path", vaultPath, "field", field)
	return fmt.Sprintf("%v", val), nil
}

// CheckAccessibility verifies the secret is accessible without fetching its value.
func (r *vaultResolver) CheckAccessibility(ctx context.Context, ref core.SecretRef) error {
	client, err := r.getClient(ctx, ref)
	if err != nil {
		return err
	}

	if _, err := client.LookupSelf(ctx); err != nil {
		return fmt.Errorf("vault token lookup failed: %w", err)
	}

	_, err = r.Resolve(ctx, ref)
	return err
}

func (r *vaultResolver) getClient(ctx context.Context, ref core.SecretRef) (vaultClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Prioritize client injected for testing
	if r.client != nil {
		return r.client, nil
	}

	settings := r.resolveClientSettings(ctx, ref)

	// 3. Check cache
	if r.cachedClient != nil && r.cachedSettings == settings {
		return r.cachedClient, nil
	}

	// 4. Create new client
	client, err := r.newClient(settings)
	if err != nil {
		return nil, err
	}

	r.cachedClient = client
	r.cachedSettings = settings

	return client, nil
}

func (r *vaultResolver) resolveClientSettings(ctx context.Context, ref core.SecretRef) vaultClientSettings {
	settings := vaultClientSettings{
		address: api.DefaultAddress,
	}

	cfg := config.GetConfig(ctx)
	if cfg.Secrets.Vault.Address != "" {
		settings.address = cfg.Secrets.Vault.Address
	}
	if cfg.Secrets.Vault.Token != "" {
		settings.token = cfg.Secrets.Vault.Token
	}
	if addr := ref.Options["vault_address"]; addr != "" {
		settings.address = addr
	}
	if token := ref.Options["vault_token"]; token != "" {
		settings.token = token
	}

	return settings
}

func (r *vaultResolver) newClient(settings vaultClientSettings) (vaultClient, error) {
	if r.clientFactory != nil {
		return r.clientFactory(settings)
	}

	cfg := api.DefaultConfig()
	cfg.Address = settings.address

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	if settings.token != "" {
		client.SetToken(settings.token)
	} else {
		client.ClearToken()
	}

	return &realVaultClient{client: client}, nil
}

func (r *vaultResolver) parseKey(ref core.SecretRef) (string, string) {
	if f, ok := ref.Options["field"]; ok && f != "" {
		return ref.Key, f
	}

	key := strings.TrimSuffix(ref.Key, "/")
	if lastSlash := strings.LastIndex(key, "/"); lastSlash != -1 {
		return key[:lastSlash], key[lastSlash+1:]
	}

	return key, "value"
}

// vaultClient interface and implementations

type vaultClient interface {
	Read(ctx context.Context, path string) (map[string]any, error)
	LookupSelf(ctx context.Context) (map[string]any, error)
}

type realVaultClient struct {
	client *api.Client
}

func (c *realVaultClient) Read(_ context.Context, path string) (map[string]any, error) {
	secret, err := c.client.Logical().Read(path)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	return secret.Data, nil
}

func (c *realVaultClient) LookupSelf(_ context.Context) (map[string]any, error) {
	secret, err := c.client.Auth().Token().LookupSelf()
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	return secret.Data, nil
}

func getKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
