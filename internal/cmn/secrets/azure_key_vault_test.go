// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type azureTestCredential struct{}

func (azureTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type azureTestClient struct {
	getSecret func(context.Context, string, string) (*string, error)
}

func (c *azureTestClient) GetSecret(ctx context.Context, name, version string) (*string, error) {
	return c.getSecret(ctx, name, version)
}

func TestAzureKeyVaultResolverValidate(t *testing.T) {
	resolver := &azureKeyVaultResolver{}
	tests := []struct {
		name    string
		ref     core.SecretRef
		wantErr string
	}{
		{name: "ShortName", ref: core.SecretRef{Key: "database-password"}},
		{name: "ShortNameWithVault", ref: core.SecretRef{Key: "database-password", Options: map[string]string{"vault_url": "https://example.vault.azure.net/"}}},
		{name: "FullURL", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/database-password"}},
		{name: "FullVersionURL", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/database-password/version-id"}},
		{name: "Empty", ref: core.SecretRef{}, wantErr: "key"},
		{name: "ShortNameWithSlash", ref: core.SecretRef{Key: "team/database-password"}, wantErr: "must not contain slashes"},
		{name: "HTTP", ref: core.SecretRef{Key: "http://example.vault.azure.net/secrets/name"}, wantErr: "HTTPS"},
		{name: "Query", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/name?api-version=1"}, wantErr: "only an HTTPS host and path"},
		{name: "BareQuery", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/name?"}, wantErr: "only an HTTPS host and path"},
		{name: "ExtraSegment", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/name/version/extra"}, wantErr: "URL path"},
		{name: "VersionConflict", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/name/url-version", Options: map[string]string{"version": "option-version"}}, wantErr: "conflicts"},
		{name: "VaultConflict", ref: core.SecretRef{Key: "https://example.vault.azure.net/secrets/name", Options: map[string]string{"vault_url": "https://other.vault.azure.net"}}, wantErr: "cannot be used"},
		{name: "VaultPath", ref: core.SecretRef{Key: "name", Options: map[string]string{"vault_url": "https://example.vault.azure.net/path"}}, wantErr: "path must be empty"},
		{name: "ArbitraryHost", ref: core.SecretRef{Key: "https://example.com/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "NestedHost", ref: core.SecretRef{Key: "https://attacker.example.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "Localhost", ref: core.SecretRef{Key: "https://localhost/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "IPAddress", ref: core.SecretRef{Key: "https://127.0.0.1/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "CustomPort", ref: core.SecretRef{Key: "https://example.vault.azure.net:8443/secrets/name"}, wantErr: "port must be 443"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolver.Validate(tc.ref)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAzureKeyVaultResolverRegistered(t *testing.T) {
	resolver := NewRegistry().Get(azureKeyVaultProvider)
	require.NotNil(t, resolver)
	assert.Equal(t, azureKeyVaultProvider, resolver.Name())
}

func TestAzureKeyVaultResolverResolve(t *testing.T) {
	var mu sync.Mutex
	var vaultURLs []string
	var requests []string
	value := `{"token":"resolved","enabled":true}`
	resolver := &azureKeyVaultResolver{
		credential: azureTestCredential{},
		clientFactory: func(vaultURL string, _ azcore.TokenCredential) (azureSecretClient, error) {
			mu.Lock()
			vaultURLs = append(vaultURLs, vaultURL)
			mu.Unlock()
			return &azureTestClient{getSecret: func(_ context.Context, name, version string) (*string, error) {
				mu.Lock()
				requests = append(requests, name+":"+version)
				mu.Unlock()
				return &value, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{Azure: config.AzureSecretsConfig{VaultURL: "https://default.vault.azure.net/"}},
	})

	got, err := resolver.Resolve(ctx, core.SecretRef{
		Key: "database-password", Options: map[string]string{"version": "v1", "field": "token"},
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)

	got, err = resolver.Resolve(ctx, core.SecretRef{
		Key: "database-password", Options: map[string]string{"vault_url": "https://OTHER.vault.azure.net", "field": "enabled"},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", got)

	got, err = resolver.Resolve(ctx, core.SecretRef{Key: "https://full.vault.azure.net/secrets/name/v2"})
	require.NoError(t, err)
	assert.Equal(t, value, got)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{
		"https://default.vault.azure.net",
		"https://other.vault.azure.net",
		"https://full.vault.azure.net",
	}, vaultURLs)
	assert.Equal(t, []string{"database-password:v1", "database-password:", "name:v2"}, requests)
}

func TestAzureKeyVaultResolverErrors(t *testing.T) {
	t.Run("MissingVaultURL", func(t *testing.T) {
		resolver := &azureKeyVaultResolver{}
		_, err := resolver.Resolve(context.Background(), core.SecretRef{Key: "name"})
		require.ErrorContains(t, err, "vault URL is required")
	})

	t.Run("MissingValue", func(t *testing.T) {
		resolver := &azureKeyVaultResolver{
			credential: azureTestCredential{},
			clientFactory: func(string, azcore.TokenCredential) (azureSecretClient, error) {
				return &azureTestClient{getSecret: func(context.Context, string, string) (*string, error) {
					return nil, nil
				}}, nil
			},
		}
		_, err := resolver.Resolve(context.Background(), core.SecretRef{Key: "https://example.vault.azure.net/secrets/name"})
		require.ErrorContains(t, err, "has no value")
	})

	t.Run("ReadError", func(t *testing.T) {
		resolver := &azureKeyVaultResolver{
			credential: azureTestCredential{},
			clientFactory: func(string, azcore.TokenCredential) (azureSecretClient, error) {
				return &azureTestClient{getSecret: func(context.Context, string, string) (*string, error) {
					return nil, fmt.Errorf("permission denied")
				}}, nil
			},
		}
		_, err := resolver.Resolve(context.Background(), core.SecretRef{Key: "https://example.vault.azure.net/secrets/name"})
		require.ErrorContains(t, err, "failed to read")
	})
}

func TestAzureKeyVaultResolverCachesClients(t *testing.T) {
	var factoryCalls int
	value := "secret"
	resolver := &azureKeyVaultResolver{
		credential: azureTestCredential{},
		clientFactory: func(string, azcore.TokenCredential) (azureSecretClient, error) {
			factoryCalls++
			return &azureTestClient{getSecret: func(context.Context, string, string) (*string, error) {
				return &value, nil
			}}, nil
		},
	}
	ref := core.SecretRef{Key: "https://example.vault.azure.net/secrets/name"}

	for range 2 {
		_, err := resolver.Resolve(context.Background(), ref)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, factoryCalls)
}
