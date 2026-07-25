// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	alibabakms "github.com/alibabacloud-go/kms-20160120/v4/client"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type alibabaKMSTestClient struct {
	getSecretValue func(context.Context, *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error)
}

func (c *alibabaKMSTestClient) GetSecretValue(ctx context.Context, request *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
	return c.getSecretValue(ctx, request)
}

func TestAlibabaKMSResolverValidate(t *testing.T) {
	resolver := &alibabaKMSResolver{}
	tests := []struct {
		name    string
		key     string
		options map[string]string
		wantErr string
	}{
		{name: "Name", key: "production/database-password"},
		{name: "ManagedSecretName", key: "acs/ram/user/example"},
		{name: "ARN", key: "acs:kms:cn-hangzhou:1234567890123456:secret/production/database-password"},
		{name: "Empty", wantErr: "key"},
		{name: "InvalidName", key: "database password", wantErr: "unsupported characters"},
		{name: "NameTooLong", key: strings.Repeat("a", 193), wantErr: "192"},
		{name: "MalformedARN", key: "acs:not-valid", wantErr: "KMS secret"},
		{name: "WrongService", key: "acs:ecs:cn-hangzhou:1234567890123456:secret/name", wantErr: "KMS secret"},
		{name: "MissingRegion", key: "acs:kms::1234567890123456:secret/name", wantErr: "KMS secret"},
		{name: "MissingAccount", key: "acs:kms:cn-hangzhou::secret/name", wantErr: "KMS secret"},
		{name: "WrongResource", key: "acs:kms:cn-hangzhou:1234567890123456:key/name", wantErr: "KMS secret"},
		{name: "RegionWhitespace", key: "acs:kms:cn-hangzhou:1234567890123456:secret/name", options: map[string]string{"region": " cn-hangzhou "}},
		{name: "RegionConflict", key: "acs:kms:cn-hangzhou:1234567890123456:secret/name", options: map[string]string{"region": "cn-shanghai"}, wantErr: "conflicts"},
		{name: "UnsupportedOption", key: "database-password", options: map[string]string{"profile": "production"}, wantErr: `unsupported option "profile"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolver.Validate(core.SecretRef{Key: tc.key, Options: tc.options})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAlibabaKMSResolverRegistered(t *testing.T) {
	resolver := NewRegistry().Get(alibabaKMSProvider)
	require.NotNil(t, resolver)
	assert.Equal(t, alibabaKMSProvider, resolver.Name())
}

func TestAlibabaKMSResolverResolve(t *testing.T) {
	var settings []alibabaKMSClientSettings
	var requests []*alibabakms.GetSecretValueRequest
	value := `{"token":"resolved","enabled":true}`
	resolver := &alibabaKMSResolver{
		clientFactory: func(clientSettings alibabaKMSClientSettings) (alibabaKMSClient, error) {
			settings = append(settings, clientSettings)
			return &alibabaKMSTestClient{getSecretValue: func(_ context.Context, request *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
				requests = append(requests, request)
				return &alibabakms.GetSecretValueResponse{
					Body: &alibabakms.GetSecretValueResponseBody{SecretData: &value},
				}, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{Alibaba: config.AlibabaSecretsConfig{Region: " cn-hangzhou "}},
	})

	got, err := resolver.Resolve(ctx, core.SecretRef{
		Key: " production/database-password ",
		Options: map[string]string{
			"version_id":    " version-1 ",
			"version_stage": " custom-stage ",
			"field":         "token",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)

	got, err = resolver.Resolve(ctx, core.SecretRef{
		Key:     "production/database-password",
		Options: map[string]string{"region": " ap-southeast-1 ", "field": "enabled"},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", got)

	arn := "acs:kms:us-west-1:1234567890123456:secret/production/database-password"
	got, err = resolver.Resolve(ctx, core.SecretRef{Key: arn})
	require.NoError(t, err)
	assert.Equal(t, value, got)

	assert.Equal(t, []alibabaKMSClientSettings{
		{region: "cn-hangzhou"},
		{region: "ap-southeast-1"},
		{region: "us-west-1"},
	}, settings)
	require.Len(t, requests, 3)
	assert.Equal(t, "production/database-password", *requests[0].SecretName)
	assert.Equal(t, " version-1 ", *requests[0].VersionId)
	assert.Equal(t, " custom-stage ", *requests[0].VersionStage)
	assert.Equal(t, arn, *requests[2].SecretName)
}

func TestAlibabaKMSResolverConfiguredEndpoint(t *testing.T) {
	var settings alibabaKMSClientSettings
	value := "secret"
	resolver := &alibabaKMSResolver{
		clientFactory: func(clientSettings alibabaKMSClientSettings) (alibabaKMSClient, error) {
			settings = clientSettings
			return &alibabaKMSTestClient{getSecretValue: func(context.Context, *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
				return &alibabakms.GetSecretValueResponse{
					Body: &alibabakms.GetSecretValueResponseBody{SecretData: &value},
				}, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{Alibaba: config.AlibabaSecretsConfig{
			Region:   "cn-hangzhou",
			Endpoint: " KST-EXAMPLE.cryptoservice.kms.aliyuncs.com ",
			CAFile:   "/etc/alibaba-kms/ca.pem",
		}},
	})

	got, err := resolver.Resolve(ctx, core.SecretRef{Key: "database-password"})
	require.NoError(t, err)
	assert.Equal(t, value, got)
	assert.Equal(t, alibabaKMSClientSettings{
		region:   "cn-hangzhou",
		endpoint: "kst-example.cryptoservice.kms.aliyuncs.com",
		caFile:   "/etc/alibaba-kms/ca.pem",
	}, settings)
}

func TestAlibabaKMSResolverErrors(t *testing.T) {
	tests := []struct {
		name           string
		providerConfig config.AlibabaSecretsConfig
		options        map[string]string
		output         *alibabakms.GetSecretValueResponse
		err            error
		wantErr        string
	}{
		{name: "MissingRegion", wantErr: "region is required"},
		{name: "InvalidEndpoint", providerConfig: config.AlibabaSecretsConfig{Endpoint: "https://kms.cn-hangzhou.aliyuncs.com"}, wantErr: "without a URL scheme"},
		{name: "CAWithoutEndpoint", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou", CAFile: "/tmp/ca.pem"}, wantErr: "requires a configured endpoint"},
		{name: "EndpointRegionConflict", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou", Endpoint: "kms-vpc.cn-hangzhou.aliyuncs.com"}, options: map[string]string{"region": "cn-shanghai"}, wantErr: "conflicts with configured"},
		{name: "ReadError", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou"}, err: fmt.Errorf("permission denied"), wantErr: "failed to read"},
		{name: "NilOutput", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou"}, wantErr: "no result returned"},
		{name: "NilBody", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou"}, output: &alibabakms.GetSecretValueResponse{}, wantErr: "no result returned"},
		{name: "MissingValue", providerConfig: config.AlibabaSecretsConfig{Region: "cn-hangzhou"}, output: &alibabakms.GetSecretValueResponse{Body: &alibabakms.GetSecretValueResponseBody{}}, wantErr: "has no value"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &alibabaKMSResolver{
				clientFactory: func(alibabaKMSClientSettings) (alibabaKMSClient, error) {
					return &alibabaKMSTestClient{getSecretValue: func(context.Context, *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
						return tc.output, tc.err
					}}, nil
				},
			}
			ctx := config.WithConfig(context.Background(), &config.Config{
				Secrets: config.SecretsConfig{Alibaba: tc.providerConfig},
			})
			_, err := resolver.Resolve(ctx, core.SecretRef{Key: "name", Options: tc.options})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestAlibabaKMSResolverCachesClients(t *testing.T) {
	var factoryCalls int
	value := "secret"
	resolver := &alibabaKMSResolver{
		clientFactory: func(alibabaKMSClientSettings) (alibabaKMSClient, error) {
			factoryCalls++
			return &alibabaKMSTestClient{getSecretValue: func(context.Context, *alibabakms.GetSecretValueRequest) (*alibabakms.GetSecretValueResponse, error) {
				return &alibabakms.GetSecretValueResponse{
					Body: &alibabakms.GetSecretValueResponseBody{SecretData: &value},
				}, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{Alibaba: config.AlibabaSecretsConfig{Region: "cn-hangzhou"}},
	})

	for range 2 {
		_, err := resolver.Resolve(ctx, core.SecretRef{Key: "name"})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, factoryCalls)
}

func TestNewAlibabaKMSClientMissingCAFile(t *testing.T) {
	_, err := newAlibabaKMSClient(alibabaKMSClientSettings{
		endpoint: "kst-example.cryptoservice.kms.aliyuncs.com",
		caFile:   filepath.Join(t.TempDir(), "missing.pem"),
	}, nil)
	require.ErrorContains(t, err, "failed to read CA file")
}
