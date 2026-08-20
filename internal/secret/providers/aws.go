// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const awsSecretsManagerProvider = "aws"

func init() {
	registerResolver(awsSecretsManagerProvider, func(_ []string) Resolver {
		return &awsSecretsManagerResolver{}
	})
}

type awsSecretsManagerResolver struct {
	mu            sync.Mutex
	clientFactory func(context.Context) (awsSecretsManagerClient, error)
	client        awsSecretsManagerClient
}

type awsSecretReference struct {
	key    string
	region string
}

func (r *awsSecretsManagerResolver) Name() string {
	return awsSecretsManagerProvider
}

func (r *awsSecretsManagerResolver) Validate(ref secretref.Ref) error {
	_, err := parseAWSSecretReference(ref)
	return err
}

func parseAWSSecretReference(ref secretref.Ref) (awsSecretReference, error) {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return awsSecretReference{}, fmt.Errorf("key (AWS Secrets Manager secret name or ARN) is required")
	}
	parsed, isARN, err := parseAWSSecretsManagerARN(key)
	if err != nil {
		return awsSecretReference{}, err
	}
	region := strings.TrimSpace(ref.Options["region"])
	if isARN && region != "" && region != parsed.Region {
		return awsSecretReference{}, fmt.Errorf("options.region %q conflicts with AWS Secrets Manager ARN region %q", region, parsed.Region)
	}
	if isARN {
		region = parsed.Region
	}
	for option := range ref.Options {
		switch option {
		case "field", "region", "version_id", "version_stage":
		default:
			return awsSecretReference{}, fmt.Errorf("unsupported option %q", option)
		}
	}
	return awsSecretReference{key: key, region: region}, nil
}

func (r *awsSecretsManagerResolver) CheckCapability(secretref.Ref) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *awsSecretsManagerResolver) Resolve(ctx context.Context, ref secretref.Ref) (string, error) {
	parsed, err := parseAWSSecretReference(ref)
	if err != nil {
		return "", err
	}
	region := parsed.region
	if region == "" {
		region = strings.TrimSpace(config.GetConfig(ctx).Secrets.AWS.Region)
	}
	client, err := r.getClient(ctx)
	if err != nil {
		return "", err
	}

	input := &secretsmanager.GetSecretValueInput{SecretId: aws.String(parsed.key)}
	if versionID := ref.Options["version_id"]; versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	if versionStage := ref.Options["version_stage"]; versionStage != "" {
		input.VersionStage = aws.String(versionStage)
	}
	var options []func(*secretsmanager.Options)
	if region != "" {
		options = append(options, func(options *secretsmanager.Options) {
			options.Region = region
		})
	}
	output, err := client.GetSecretValue(ctx, input, options...)
	if err != nil {
		if _, ok := errors.AsType[*types.ResourceNotFoundException](err); ok {
			return "", fmt.Errorf("AWS Secrets Manager secret %q was not found: %w", parsed.key, err)
		}
		return "", fmt.Errorf("failed to read AWS Secrets Manager secret %q: %w", parsed.key, err)
	}
	if output == nil {
		return "", fmt.Errorf("AWS Secrets Manager returned no result for secret %q", parsed.key)
	}

	var value string
	switch {
	case output.SecretString != nil:
		value = *output.SecretString
	case output.SecretBinary != nil:
		value = base64.StdEncoding.EncodeToString(output.SecretBinary)
	default:
		return "", fmt.Errorf("AWS Secrets Manager secret %q has no value", parsed.key)
	}
	return selectJSONField(value, ref.Options["field"])
}

func (r *awsSecretsManagerResolver) CheckAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *awsSecretsManagerResolver) getClient(ctx context.Context) (awsSecretsManagerClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return r.client, nil
	}

	factory := r.clientFactory
	if factory == nil {
		factory = newAWSSecretsManagerClient
	}
	client, err := factory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS Secrets Manager client: %w", err)
	}
	r.client = client
	return r.client, nil
}

func parseAWSSecretsManagerARN(key string) (awsarn.ARN, bool, error) {
	if !strings.HasPrefix(key, "arn:") {
		return awsarn.ARN{}, false, nil
	}
	parsed, err := awsarn.Parse(key)
	if err != nil {
		return awsarn.ARN{}, true, fmt.Errorf("invalid AWS Secrets Manager ARN: %w", err)
	}
	if parsed.Service != "secretsmanager" || parsed.Region == "" || parsed.AccountID == "" || !strings.HasPrefix(parsed.Resource, "secret:") {
		return awsarn.ARN{}, true, fmt.Errorf("ARN must identify an AWS Secrets Manager secret")
	}
	return parsed, true, nil
}

type awsSecretsManagerClient interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

func newAWSSecretsManagerClient(ctx context.Context) (awsSecretsManagerClient, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(cfg), nil
}
