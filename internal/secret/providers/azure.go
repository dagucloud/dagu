// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

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
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const azureKeyVaultProvider = "azure"

var (
	azureVaultNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	azureSecretNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,127}$`)
	azureVaultDNSSuffixes  = []string{
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

func (r *azureKeyVaultResolver) Validate(ref secretref.Ref) error {
	_, err := parseAzureSecretReference(ref, "")
	return err
}

func (r *azureKeyVaultResolver) CheckCapability(secretref.Ref) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *azureKeyVaultResolver) Resolve(ctx context.Context, ref secretref.Ref) (string, error) {
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
			return "", fmt.Errorf("secret %q was not found in Azure Key Vault: %w", parsed.name, err)
		}
		return "", fmt.Errorf("failed to read Azure Key Vault secret %q: %w", parsed.name, err)
	}
	if value == nil {
		return "", fmt.Errorf("secret %q in Azure Key Vault has no value", parsed.name)
	}
	return selectJSONField(*value, ref.Options["field"])
}

func (r *azureKeyVaultResolver) CheckAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *azureKeyVaultResolver) getClient(vaultURL string) (azureSecretClient, error) {
	r.mu.Lock()
	if client := r.clients[vaultURL]; client != nil {
		r.mu.Unlock()
		return client, nil
	}
	r.mu.Unlock()

	credential, err := r.getCredential()
	if err != nil {
		return nil, err
	}

	clientFactory := r.clientFactory
	if clientFactory == nil {
		clientFactory = newAzureSecretClient
	}
	client, err := clientFactory(vaultURL, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Key Vault client: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = make(map[string]azureSecretClient)
	}
	if existing := r.clients[vaultURL]; existing != nil {
		return existing, nil
	}
	r.clients[vaultURL] = client
	return client, nil
}

func (r *azureKeyVaultResolver) getCredential() (azcore.TokenCredential, error) {
	r.mu.Lock()
	if r.credential != nil {
		credential := r.credential
		r.mu.Unlock()
		return credential, nil
	}
	r.mu.Unlock()

	credentialFactory := r.credentialFactory
	if credentialFactory == nil {
		credentialFactory = func() (azcore.TokenCredential, error) {
			return azidentity.NewDefaultAzureCredential(nil)
		}
	}
	credential, err := credentialFactory()
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.credential != nil {
		return r.credential, nil
	}
	r.credential = credential
	return credential, nil
}

func parseAzureSecretReference(ref secretref.Ref, defaultVaultURL string) (azureSecretReference, error) {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return azureSecretReference{}, fmt.Errorf("key (Azure Key Vault secret name or URL) is required")
	}
	for option := range ref.Options {
		switch option {
		case "field", "vault_url", "version":
		default:
			return azureSecretReference{}, fmt.Errorf("unsupported option %q", option)
		}
	}
	if version := ref.Options["version"]; version != "" && !isSafeAzureSecretVersion(version) {
		return azureSecretReference{}, fmt.Errorf("options.version for Azure Key Vault must not contain slashes or relative path segments")
	}

	if strings.Contains(key, "://") {
		if ref.Options["vault_url"] != "" {
			return azureSecretReference{}, fmt.Errorf("options.vault_url cannot be used with an Azure Key Vault secret URL")
		}
		return parseAzureSecretURL(key, ref.Options["version"])
	}
	if !isValidAzureSecretName(key) {
		return azureSecretReference{}, fmt.Errorf("secret name for Azure Key Vault must contain only 1-127 alphanumeric characters or hyphens")
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
	if len(segments) < 2 || len(segments) > 3 || !strings.EqualFold(segments[0], "secrets") || segments[1] == "" {
		return azureSecretReference{}, fmt.Errorf("secret URL path for Azure Key Vault must be /secrets/{name} or /secrets/{name}/{version}")
	}
	name, err := url.PathUnescape(segments[1])
	if err != nil || !isValidAzureSecretName(name) {
		return azureSecretReference{}, fmt.Errorf("secret URL for Azure Key Vault contains an invalid secret name")
	}
	version := optionVersion
	if len(segments) == 3 {
		if optionVersion != "" {
			return azureSecretReference{}, fmt.Errorf("options.version conflicts with the version in the Azure Key Vault secret URL")
		}
		version, err = url.PathUnescape(segments[2])
		if err != nil || !isSafeAzureSecretVersion(version) {
			return azureSecretReference{}, fmt.Errorf("secret URL for Azure Key Vault contains an invalid version")
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
	if u.Host == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("URL must contain only an HTTPS host and path")
	}
	if port := u.Port(); port != "" && port != "443" {
		return "", fmt.Errorf("port must be 443")
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range azureVaultDNSSuffixes {
		if vaultName, ok := strings.CutSuffix(host, suffix); ok && isValidAzureVaultName(vaultName) {
			return host, nil
		}
	}
	return "", fmt.Errorf("host must be an Azure Key Vault endpoint")
}

func isValidAzureVaultName(name string) bool {
	return len(name) >= 3 && len(name) <= 24 &&
		azureVaultNamePattern.MatchString(name) && !strings.Contains(name, "--")
}

func isValidAzureSecretName(name string) bool {
	return azureSecretNamePattern.MatchString(name)
}

func isSafeAzureSecretVersion(version string) bool {
	return version != "" && version != "." && version != ".." && !strings.Contains(version, "/")
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
