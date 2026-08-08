// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type awsSecretsManagerTestClient struct {
	getSecretValue func(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

func (c *awsSecretsManagerTestClient) GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, options ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return c.getSecretValue(ctx, input, options...)
}

func TestAWSSecretsManagerResolverValidate(t *testing.T) {
	resolver := &awsSecretsManagerResolver{}
	tests := []struct {
		name    string
		key     string
		options map[string]string
		wantErr string
	}{
		{name: "Name", key: "database-password"},
		{name: "ARN", key: "arn:aws:secretsmanager:us-east-1:123456789012:secret:database-password-AbCdEf"},
		{name: "Empty", wantErr: "key"},
		{name: "MalformedARN", key: "arn:not-valid", wantErr: "invalid"},
		{name: "WrongService", key: "arn:aws:ssm:us-east-1:123456789012:parameter/name", wantErr: "Secrets Manager"},
		{name: "MissingRegion", key: "arn:aws:secretsmanager::123456789012:secret:name", wantErr: "Secrets Manager"},
		{name: "RegionWhitespace", key: "arn:aws:secretsmanager:us-east-1:123456789012:secret:name", options: map[string]string{"region": " us-east-1 "}},
		{name: "RegionConflict", key: "arn:aws:secretsmanager:us-east-1:123456789012:secret:name", options: map[string]string{"region": "us-west-2"}, wantErr: "conflicts"},
		{name: "UnsupportedOption", key: "database-password", options: map[string]string{"profile": "production"}, wantErr: `unsupported option "profile"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolver.Validate(secretref.Ref{Key: tc.key, Options: tc.options})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAWSSecretsManagerResolverRegistered(t *testing.T) {
	resolver := NewRegistry().Get(awsSecretsManagerProvider)
	require.NotNil(t, resolver)
	assert.Equal(t, awsSecretsManagerProvider, resolver.Name())
}

func TestAWSSecretsManagerResolverResolve(t *testing.T) {
	var regions []string
	var inputs []*secretsmanager.GetSecretValueInput
	var factoryCalls int
	value := `{"token":"resolved","enabled":true}`
	resolver := &awsSecretsManagerResolver{
		clientFactory: func(context.Context) (awsSecretsManagerClient, error) {
			factoryCalls++
			return &awsSecretsManagerTestClient{getSecretValue: func(_ context.Context, input *secretsmanager.GetSecretValueInput, options ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
				requestOptions := secretsmanager.Options{}
				for _, option := range options {
					option(&requestOptions)
				}
				regions = append(regions, requestOptions.Region)
				inputs = append(inputs, input)
				return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(value)}, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{AWS: config.AWSSecretsConfig{Region: " us-west-2 "}},
	})

	got, err := resolver.Resolve(ctx, secretref.Ref{
		Key: " database-password ",
		Options: map[string]string{
			"version_id":    " 0123456789abcdef0123456789abcdef ",
			"version_stage": " custom-stage ",
			"field":         "token",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)

	got, err = resolver.Resolve(ctx, secretref.Ref{
		Key:     "database-password",
		Options: map[string]string{"region": " eu-west-1 ", "field": "enabled"},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", got)

	arn := "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:database-password-AbCdEf"
	got, err = resolver.Resolve(ctx, secretref.Ref{Key: arn})
	require.NoError(t, err)
	assert.Equal(t, value, got)

	assert.Equal(t, []string{"us-west-2", "eu-west-1", "ap-northeast-1"}, regions)
	assert.Equal(t, 1, factoryCalls)
	require.Len(t, inputs, 3)
	assert.Equal(t, "database-password", aws.ToString(inputs[0].SecretId))
	assert.Equal(t, " 0123456789abcdef0123456789abcdef ", aws.ToString(inputs[0].VersionId))
	assert.Equal(t, " custom-stage ", aws.ToString(inputs[0].VersionStage))
	assert.Equal(t, arn, aws.ToString(inputs[2].SecretId))
}

func TestAWSSecretsManagerResolverBinaryValue(t *testing.T) {
	resolver := &awsSecretsManagerResolver{
		clientFactory: func(context.Context) (awsSecretsManagerClient, error) {
			return &awsSecretsManagerTestClient{getSecretValue: func(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
				return &secretsmanager.GetSecretValueOutput{SecretBinary: []byte{0xff, 0x00, 0x01}}, nil
			}}, nil
		},
	}

	got, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "name"})
	require.NoError(t, err)
	assert.Equal(t, "/wAB", got)
}

func TestAWSSecretsManagerResolverErrors(t *testing.T) {
	tests := []struct {
		name    string
		output  *secretsmanager.GetSecretValueOutput
		err     error
		wantErr string
	}{
		{name: "ReadError", err: fmt.Errorf("permission denied"), wantErr: "failed to read"},
		{name: "NilOutput", wantErr: "returned no result"},
		{name: "MissingValue", output: &secretsmanager.GetSecretValueOutput{}, wantErr: "has no value"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &awsSecretsManagerResolver{
				clientFactory: func(context.Context) (awsSecretsManagerClient, error) {
					return &awsSecretsManagerTestClient{getSecretValue: func(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
						return tc.output, tc.err
					}}, nil
				},
			}
			_, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "name"})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestAWSSecretsManagerResolverCachesClient(t *testing.T) {
	var factoryCalls int
	resolver := &awsSecretsManagerResolver{
		clientFactory: func(context.Context) (awsSecretsManagerClient, error) {
			factoryCalls++
			return &awsSecretsManagerTestClient{getSecretValue: func(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
				return &secretsmanager.GetSecretValueOutput{SecretString: aws.String("value")}, nil
			}}, nil
		},
	}

	for range 2 {
		_, err := resolver.Resolve(context.Background(), secretref.Ref{Key: "name"})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, factoryCalls)
}
