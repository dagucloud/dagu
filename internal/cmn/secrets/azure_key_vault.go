// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
)

const azureKeyVaultProvider = "azure-key-vault"

var (
	azureVaultNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)
	azureVaultDNSSuffixes = []string{
		".vault.azure.net",
		".vault.azure.cn",
		".vault.usgovcloudapi.net",
	}
)

func init() {
	registerResolver(azureKeyVaultProvider, func(_ []string) Resolver {
		return &azureKeyVaultResolver{}
	})
}

type azureKeyVaultResolver struct {
	mu                sync.Mutex
	credential        azcore.TokenCredential
	credentialFactory func() (azcore.TokenCredential, error)
	clientFactory     func(string, azcore.TokenCredential) (azureSecretClient, error)
	clients           map[string]azureSecretClient
}

type azureSecretReference struct {
	vaultURL string
	name     string
	version  string
}

func (r *azureKeyVaultResolver) Name() string {
	return azureKeyVaultProvider
}

func (r *azureKeyVaultResolver) Validate(ref core.SecretRef) error {
	_, err := parseAzureSecretReference(ref, "")
	return err
}

func (r *azureKeyVaultResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *azureKeyVaultResolver) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	defaultVaultURL := config.GetConfig(ctx).Secrets.Azure.VaultURL
	parsed, err := parseAzureSecretReference(ref, defaultVaultURL)
	if err != nil {
		return "", err
	}
	if parsed.vaultURL == "" {
		return "", fmt.Errorf("vault URL is required for Azure Key Vault secret %q", parsed.name)
	}

	client, err := r.getClient(parsed.vaultURL)
	if err != nil {
		return "", err
	}
	value, err := client.GetSecret(ctx, parsed.name, parsed.version)
	if err != nil {
		var responseErr *azcore.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("Azure Key Vault secret %q was not found: %w", parsed.name, err)
		}
		return "", fmt.Errorf("failed to read Azure Key Vault secret %q: %w", parsed.name, err)
	}
	if value == nil {
		return "", fmt.Errorf("Azure Key Vault secret %q has no value", parsed.name)
	}
	return selectJSONField(*value, ref.Options["field"])
}

func (r *azureKeyVaultResolver) CheckAccessibility(ctx context.Context, ref core.SecretRef) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *azureKeyVaultResolver) getClient(vaultURL string) (azureSecretClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client := r.clients[vaultURL]; client != nil {
		return client, nil
	}
	credential := r.credential
	if credential == nil {
		factory := r.credentialFactory
		if factory == nil {
			factory = func() (azcore.TokenCredential, error) {
				return azidentity.NewDefaultAzureCredential(nil)
			}
		}
		var err error
		credential, err = factory()
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}
		r.credential = credential
	}

	factory := r.clientFactory
	if factory == nil {
		factory = newAzureSecretClient
	}
	client, err := factory(vaultURL, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Key Vault client: %w", err)
	}
	if r.clients == nil {
		r.clients = make(map[string]azureSecretClient)
	}
	r.clients[vaultURL] = client
	return client, nil
}

func parseAzureSecretReference(ref core.SecretRef, defaultVaultURL string) (azureSecretReference, error) {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return azureSecretReference{}, fmt.Errorf("key (Azure Key Vault secret name or URL) is required")
	}

	if strings.Contains(key, "://") {
		if ref.Options["vault_url"] != "" {
			return azureSecretReference{}, fmt.Errorf("options.vault_url cannot be used with an Azure Key Vault secret URL")
		}
		return parseAzureSecretURL(key, ref.Options["version"])
	}
	if strings.Contains(key, "/") {
		return azureSecretReference{}, fmt.Errorf("Azure Key Vault secret name must not contain slashes")
	}

	vaultURL := ref.Options["vault_url"]
	if vaultURL == "" {
		vaultURL = defaultVaultURL
	}
	if vaultURL == "" {
		return azureSecretReference{name: key, version: ref.Options["version"]}, nil
	}
	normalized, err := normalizeAzureVaultURL(vaultURL)
	if err != nil {
		return azureSecretReference{}, err
	}
	return azureSecretReference{vaultURL: normalized, name: key, version: ref.Options["version"]}, nil
}

func parseAzureSecretURL(rawURL, optionVersion string) (azureSecretReference, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return azureSecretReference{}, fmt.Errorf("invalid Azure Key Vault secret URL: %w", err)
	}
	host, err := validateAzureURL(u)
	if err != nil {
		return azureSecretReference{}, fmt.Errorf("invalid Azure Key Vault secret URL: %w", err)
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) < 2 || len(segments) > 3 || segments[0] != "secrets" || segments[1] == "" {
		return azureSecretReference{}, fmt.Errorf("Azure Key Vault secret URL path must be /secrets/{name} or /secrets/{name}/{version}")
	}
	name, err := url.PathUnescape(segments[1])
	if err != nil || name == "" || strings.Contains(name, "/") {
		return azureSecretReference{}, fmt.Errorf("Azure Key Vault secret URL contains an invalid secret name")
	}
	version := optionVersion
	if len(segments) == 3 {
		if optionVersion != "" {
			return azureSecretReference{}, fmt.Errorf("options.version conflicts with the version in the Azure Key Vault secret URL")
		}
		version, err = url.PathUnescape(segments[2])
		if err != nil || version == "" || strings.Contains(version, "/") {
			return azureSecretReference{}, fmt.Errorf("Azure Key Vault secret URL contains an invalid version")
		}
	}
	vaultURL := (&url.URL{Scheme: "https", Host: host}).String()
	return azureSecretReference{vaultURL: vaultURL, name: name, version: version}, nil
}

func normalizeAzureVaultURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid Azure Key Vault vault URL: %w", err)
	}
	host, err := validateAzureURL(u)
	if err != nil {
		return "", fmt.Errorf("invalid Azure Key Vault vault URL: %w", err)
	}
	if u.EscapedPath() != "" && u.EscapedPath() != "/" {
		return "", fmt.Errorf("invalid Azure Key Vault vault URL: path must be empty")
	}
	return (&url.URL{Scheme: "https", Host: host}).String(), nil
}

func validateAzureURL(u *url.URL) (string, error) {
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("scheme must be HTTPS")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("URL must contain only an HTTPS host and path")
	}
	if port := u.Port(); port != "" && port != "443" {
		return "", fmt.Errorf("port must be 443")
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range azureVaultDNSSuffixes {
		if !strings.HasSuffix(host, suffix) {
			continue
		}
		vaultName := strings.TrimSuffix(host, suffix)
		if vaultName != "" && !strings.Contains(vaultName, ".") && azureVaultNamePattern.MatchString(vaultName) {
			return host, nil
		}
	}
	return "", fmt.Errorf("host must be an Azure Key Vault endpoint")
}

type azureSecretClient interface {
	GetSecret(ctx context.Context, name, version string) (*string, error)
}

type azureSDKSecretClient struct {
	client *azsecrets.Client
}

func newAzureSecretClient(vaultURL string, credential azcore.TokenCredential) (azureSecretClient, error) {
	client, err := azsecrets.NewClient(vaultURL, credential, nil)
	if err != nil {
		return nil, err
	}
	return &azureSDKSecretClient{client: client}, nil
}

func (c *azureSDKSecretClient) GetSecret(ctx context.Context, name, version string) (*string, error) {
	response, err := c.client.GetSecret(ctx, name, version, nil)
	if err != nil {
		return nil, err
	}
	return response.Value, nil
}
