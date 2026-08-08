// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	alibabakms "github.com/alibabacloud-go/kms-20160120/v4/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/aliyun/credentials-go/credentials"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const alibabaKMSProvider = "alibaba"

var (
	alibabaRegionPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	alibabaAccountPattern    = regexp.MustCompile(`^[0-9]+$`)
	alibabaSecretNamePattern = regexp.MustCompile(`^[A-Za-z0-9_/+=.@-]{1,192}$`)
)

func init() {
	registerResolver(alibabaKMSProvider, func(_ []string) Resolver {
		return &alibabaKMSResolver{}
	})
}

type alibabaKMSResolver struct {
	mu            sync.Mutex
	credential    credentials.Credential
	clientFactory func(alibabaKMSClientSettings) (alibabaKMSClient, error)
	clients       map[alibabaKMSClientSettings]alibabaKMSClient
}

type alibabaSecretReference struct {
	key    string
	region string
}

type alibabaKMSClientSettings struct {
	region   string
	endpoint string
	caFile   string
}

func (r *alibabaKMSResolver) Name() string {
	return alibabaKMSProvider
}

func (r *alibabaKMSResolver) Validate(ref secretref.Ref) error {
	_, err := parseAlibabaSecretReference(ref)
	return err
}

func parseAlibabaSecretReference(ref secretref.Ref) (alibabaSecretReference, error) {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return alibabaSecretReference{}, fmt.Errorf("key (Alibaba Cloud KMS secret name or ARN) is required")
	}

	arnRegion, isARN, err := parseAlibabaKMSARN(key)
	if err != nil {
		return alibabaSecretReference{}, err
	}
	if !isARN && !alibabaSecretNamePattern.MatchString(key) {
		return alibabaSecretReference{}, fmt.Errorf("secret name for Alibaba Cloud KMS contains unsupported characters or exceeds 192 characters")
	}

	for option := range ref.Options {
		switch option {
		case "field", "region", "version_id", "version_stage":
		default:
			return alibabaSecretReference{}, fmt.Errorf("unsupported option %q", option)
		}
	}

	region := strings.TrimSpace(ref.Options["region"])
	if isARN && region != "" && region != arnRegion {
		return alibabaSecretReference{}, fmt.Errorf("options.region %q conflicts with Alibaba Cloud KMS ARN region %q", region, arnRegion)
	}
	if isARN {
		region = arnRegion
	}
	return alibabaSecretReference{key: key, region: region}, nil
}

func parseAlibabaKMSARN(key string) (region string, isARN bool, err error) {
	if !strings.HasPrefix(key, "acs:") {
		return "", false, nil
	}
	parts := strings.SplitN(key, ":", 5)
	if len(parts) != 5 ||
		parts[0] != "acs" ||
		parts[1] != "kms" ||
		!alibabaRegionPattern.MatchString(parts[2]) ||
		!alibabaAccountPattern.MatchString(parts[3]) ||
		!strings.HasPrefix(parts[4], "secret/") ||
		!alibabaSecretNamePattern.MatchString(strings.TrimPrefix(parts[4], "secret/")) {
		return "", true, fmt.Errorf("ARN must identify an Alibaba Cloud KMS secret")
	}
	return parts[2], true, nil
}

func (r *alibabaKMSResolver) CheckCapability(secretref.Ref) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *alibabaKMSResolver) Resolve(ctx context.Context, ref secretref.Ref) (string, error) {
	parsed, err := parseAlibabaSecretReference(ref)
	if err != nil {
		return "", err
	}

	providerConfig := config.GetConfig(ctx).Secrets.Alibaba
	configuredRegion := strings.TrimSpace(providerConfig.Region)
	region := parsed.region
	if region == "" {
		region = configuredRegion
	}
	endpoint, err := normalizeAlibabaKMSEndpoint(providerConfig.Endpoint)
	if err != nil {
		return "", err
	}
	if endpoint != "" && parsed.region != "" && configuredRegion != "" && parsed.region != configuredRegion {
		return "", fmt.Errorf("reference region %q conflicts with configured Alibaba Cloud KMS endpoint region %q", parsed.region, configuredRegion)
	}
	caFile := providerConfig.CAFile
	if region == "" {
		return "", fmt.Errorf("region is required for Alibaba Cloud KMS secret %q", parsed.key)
	}
	if caFile != "" && endpoint == "" {
		return "", fmt.Errorf("CA file for Alibaba Cloud KMS requires a configured endpoint")
	}

	client, err := r.getClient(alibabaKMSClientSettings{
		region:   region,
		endpoint: endpoint,
		caFile:   caFile,
	})
	if err != nil {
		return "", err
	}

	request := &alibabakms.GetSecretValueRequest{SecretName: &parsed.key}
	if versionID := ref.Options["version_id"]; versionID != "" {
		request.VersionId = &versionID
	}
	if versionStage := ref.Options["version_stage"]; versionStage != "" {
		request.VersionStage = &versionStage
	}
	response, err := client.GetSecretValue(ctx, request)
	if err != nil {
		return "", fmt.Errorf("failed to read Alibaba Cloud KMS secret %q: %w", parsed.key, err)
	}
	if response == nil || response.Body == nil {
		return "", fmt.Errorf("no result returned by Alibaba Cloud KMS for secret %q", parsed.key)
	}
	if response.Body.SecretData == nil {
		return "", fmt.Errorf("secret %q in Alibaba Cloud KMS has no value", parsed.key)
	}
	return selectJSONField(*response.Body.SecretData, ref.Options["field"])
}

func (r *alibabaKMSResolver) CheckAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *alibabaKMSResolver) getClient(settings alibabaKMSClientSettings) (alibabaKMSClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client := r.clients[settings]; client != nil {
		return client, nil
	}

	var (
		client alibabaKMSClient
		err    error
	)
	if r.clientFactory != nil {
		client, err = r.clientFactory(settings)
	} else {
		if r.credential == nil {
			r.credential, err = credentials.NewCredential(nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create Alibaba Cloud credential provider: %w", err)
			}
		}
		client, err = newAlibabaKMSClient(settings, r.credential)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create Alibaba Cloud KMS client: %w", err)
	}

	if r.clients == nil {
		r.clients = make(map[alibabaKMSClientSettings]alibabaKMSClient)
	}
	r.clients[settings] = client
	return client, nil
}

func normalizeAlibabaKMSEndpoint(rawEndpoint string) (string, error) {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" {
		return "", nil
	}
	if strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("endpoint for Alibaba Cloud KMS must be a hostname without a URL scheme")
	}
	parsed, err := url.Parse("https://" + endpoint)
	if err != nil ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.Path != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Alibaba Cloud KMS endpoint %q", rawEndpoint)
	}
	return strings.ToLower(parsed.Host), nil
}

type alibabaKMSClient interface {
	GetSecretValue(context.Context, *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error)
}

type alibabaKMSClientAdapter struct {
	client *alibabakms.Client
}

func (c *alibabaKMSClientAdapter) GetSecretValue(ctx context.Context, request *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
	return c.client.GetSecretValueWithContext(ctx, request, &dara.RuntimeOptions{})
}

func newAlibabaKMSClient(settings alibabaKMSClientSettings, credential credentials.Credential) (alibabaKMSClient, error) {
	clientConfig := (&openapiutil.Config{}).SetCredential(credential)
	if settings.region != "" {
		clientConfig.SetRegionId(settings.region)
	}
	if settings.endpoint != "" {
		clientConfig.SetEndpoint(settings.endpoint)
	}
	if settings.caFile != "" {
		ca, err := os.ReadFile(settings.caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file %q: %w", settings.caFile, err)
		}
		clientConfig.SetCa(string(ca))
	}

	client, err := alibabakms.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}
	return &alibabaKMSClientAdapter{client: client}, nil
}
