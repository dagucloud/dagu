// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const azureTestVersion = "0123456789abcdef0123456789abcdef"

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
		ref     secretref.Ref
		wantErr string
	}{
		{name: "ShortName", ref: secretref.Ref{Key: "database-password"}},
		{name: "ShortNameWithVault", ref: secretref.Ref{Key: "database-password", Options: map[string]string{"vault_url": "https://example.vault.azure.net/"}}},
		{name: "FullURLCaseInsensitiveObjectType", ref: secretref.Ref{Key: "https://example.vault.azure.net/SeCrEtS/database-password"}},
		{name: "FullVersionURL", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/database-password/" + azureTestVersion}},
		{name: "Empty", ref: secretref.Ref{}, wantErr: "key"},
		{name: "ShortNameWithSlash", ref: secretref.Ref{Key: "team/database-password"}, wantErr: "secret name"},
		{name: "ShortNameWithSpace", ref: secretref.Ref{Key: "database password"}, wantErr: "secret name"},
		{name: "ShortNameTooLong", ref: secretref.Ref{Key: strings.Repeat("a", 128)}, wantErr: "secret name"},
		{name: "OptionVersion", ref: secretref.Ref{Key: "database-password", Options: map[string]string{"version": "v1"}}},
		{name: "OptionVersionWithSlash", ref: secretref.Ref{Key: "database-password", Options: map[string]string{"version": strings.Repeat("a", 31) + "/"}}, wantErr: "options.version"},
		{name: "OptionVersionRelativePath", ref: secretref.Ref{Key: "database-password", Options: map[string]string{"version": ".."}}, wantErr: "options.version"},
		{name: "HTTP", ref: secretref.Ref{Key: "http://example.vault.azure.net/secrets/name"}, wantErr: "HTTPS"},
		{name: "Query", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name?api-version=1"}, wantErr: "only an HTTPS host and path"},
		{name: "BareQuery", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name?"}, wantErr: "only an HTTPS host and path"},
		{name: "URLNameWithEncodedSpace", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/database%20password"}, wantErr: "invalid secret name"},
		{name: "ShortURLVersion", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name/v1"}},
		{name: "URLVersionRelativePath", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name/.."}, wantErr: "invalid version"},
		{name: "ExtraSegment", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name/" + azureTestVersion + "/extra"}, wantErr: "URL path"},
		{name: "VersionConflict", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name/" + azureTestVersion, Options: map[string]string{"version": "option-version"}}, wantErr: "conflicts"},
		{name: "VaultConflict", ref: secretref.Ref{Key: "https://example.vault.azure.net/secrets/name", Options: map[string]string{"vault_url": "https://other.vault.azure.net"}}, wantErr: "cannot be used"},
		{name: "VaultPath", ref: secretref.Ref{Key: "name", Options: map[string]string{"vault_url": "https://example.vault.azure.net/path"}}, wantErr: "path must be empty"},
		{name: "ArbitraryHost", ref: secretref.Ref{Key: "https://example.com/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "NestedHost", ref: secretref.Ref{Key: "https://attacker.example.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "LeadingDigit", ref: secretref.Ref{Key: "https://1example.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "LeadingHyphen", ref: secretref.Ref{Key: "https://-example.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "TrailingHyphen", ref: secretref.Ref{Key: "https://example-.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "ConsecutiveHyphens", ref: secretref.Ref{Key: "https://exam--ple.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "VaultNameTooShort", ref: secretref.Ref{Key: "https://ab.vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "VaultNameTooLong", ref: secretref.Ref{Key: "https://" + strings.Repeat("a", 25) + ".vault.azure.net/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "Localhost", ref: secretref.Ref{Key: "https://localhost/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "IPAddress", ref: secretref.Ref{Key: "https://127.0.0.1/secrets/name"}, wantErr: "Key Vault endpoint"},
		{name: "CustomPort", ref: secretref.Ref{Key: "https://example.vault.azure.net:8443/secrets/name"}, wantErr: "port must be 443"},
		{name: "UnsupportedOption", ref: secretref.Ref{Key: "database-password", Options: map[string]string{"profile": "production"}}, wantErr: `unsupported option "profile"`},
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

	got, err := resolver.Resolve(ctx, secretref.Ref{
		Key: "database-password", Options: map[string]string{"version": "v1", "field": "token"},
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)

	got, err = resolver.Resolve(ctx, secretref.Ref{
		Key: "database-password", Options: map[string]string{"vault_url": "https://OTHER.vault.azure.net", "field": "enabled"},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", got)

	got, err = resolver.Resolve(ctx, secretref.Ref{Key: "https://full.vault.azure.net/secrets/name/v2"})
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
		_, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "name"})
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
		_, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "https://example.vault.azure.net/secrets/name"})
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
		_, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "https://example.vault.azure.net/secrets/name"})
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
	ref := secretref.Ref{Key: "https://example.vault.azure.net/secrets/name"}

	for range 2 {
		_, err := resolver.Resolve(context.Background(), ref)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, factoryCalls)
}
