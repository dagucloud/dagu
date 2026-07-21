// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

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
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
)

const awsSecretsManagerProvider = "aws-secrets-manager"

func init() {
	registerResolver(awsSecretsManagerProvider, func(_ []string) Resolver {
		return &awsSecretsManagerResolver{}
	})
}

type awsSecretsManagerResolver struct {
	mu            sync.Mutex
	clientFactory func(context.Context, string) (awsSecretsManagerClient, error)
	clients       map[string]awsSecretsManagerClient
}

func (r *awsSecretsManagerResolver) Name() string {
	return awsSecretsManagerProvider
}

func (r *awsSecretsManagerResolver) Validate(ref core.SecretRef) error {
	key := strings.TrimSpace(ref.Key)
	if key == "" {
		return fmt.Errorf("key (AWS Secrets Manager secret name or ARN) is required")
	}
	parsed, isARN, err := parseAWSSecretsManagerARN(key)
	if err != nil {
		return err
	}
	if isARN && ref.Options["region"] != "" && ref.Options["region"] != parsed.Region {
		return fmt.Errorf("options.region %q conflicts with AWS Secrets Manager ARN region %q", ref.Options["region"], parsed.Region)
	}
	return nil
}

func (r *awsSecretsManagerResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

func (r *awsSecretsManagerResolver) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	key := strings.TrimSpace(ref.Key)
	region, err := resolveAWSRegion(ctx, ref, key)
	if err != nil {
		return "", err
	}
	client, err := r.getClient(ctx, region)
	if err != nil {
		return "", err
	}

	input := &secretsmanager.GetSecretValueInput{SecretId: aws.String(key)}
	if versionID := ref.Options["version_id"]; versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	if versionStage := ref.Options["version_stage"]; versionStage != "" {
		input.VersionStage = aws.String(versionStage)
	}
	output, err := client.GetSecretValue(ctx, input)
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return "", fmt.Errorf("AWS Secrets Manager secret %q was not found: %w", key, err)
		}
		return "", fmt.Errorf("failed to read AWS Secrets Manager secret %q: %w", key, err)
	}
	if output == nil {
		return "", fmt.Errorf("AWS Secrets Manager returned no result for secret %q", key)
	}

	var value string
	switch {
	case output.SecretString != nil:
		value = *output.SecretString
	case output.SecretBinary != nil:
		value = base64.StdEncoding.EncodeToString(output.SecretBinary)
	default:
		return "", fmt.Errorf("AWS Secrets Manager secret %q has no value", key)
	}
	return selectJSONField(value, ref.Options["field"])
}

func (r *awsSecretsManagerResolver) CheckAccessibility(ctx context.Context, ref core.SecretRef) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *awsSecretsManagerResolver) getClient(ctx context.Context, region string) (awsSecretsManagerClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client := r.clients[region]; client != nil {
		return client, nil
	}
	factory := r.clientFactory
	if factory == nil {
		factory = newAWSSecretsManagerClient
	}
	client, err := factory(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS Secrets Manager client: %w", err)
	}
	if r.clients == nil {
		r.clients = make(map[string]awsSecretsManagerClient)
	}
	r.clients[region] = client
	return client, nil
}

func resolveAWSRegion(ctx context.Context, ref core.SecretRef, key string) (string, error) {
	parsed, isARN, err := parseAWSSecretsManagerARN(key)
	if err != nil {
		return "", err
	}
	region := ref.Options["region"]
	if isARN {
		if region != "" && region != parsed.Region {
			return "", fmt.Errorf("options.region %q conflicts with AWS Secrets Manager ARN region %q", region, parsed.Region)
		}
		return parsed.Region, nil
	}
	if region != "" {
		return region, nil
	}
	return config.GetConfig(ctx).Secrets.AWS.Region, nil
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

func newAWSSecretsManagerClient(ctx context.Context, region string) (awsSecretsManagerClient, error) {
	var options []func(*awsconfig.LoadOptions) error
	if region != "" {
		options = append(options, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(cfg), nil
}
