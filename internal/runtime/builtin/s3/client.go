// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// createClient creates an S3 client based on the configuration.
func createClient(_ context.Context, cfg *Config) (*awss3.Client, error) {
	var credentialProvider aws.CredentialsProvider
	switch {
	case cfg.AccessKeyID != "" && cfg.SecretAccessKey != "":
		credentialProvider = credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			cfg.SessionToken,
		)
	default:
		credentialProvider = configCredentialsProvider{profile: cfg.Profile}
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	awsCfg := aws.Config{
		Region:      region,
		Credentials: aws.NewCredentialsCache(credentialProvider),
	}

	return awss3.NewFromConfig(awsCfg, func(options *awss3.Options) {
		if endpoint := endpointURL(cfg); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
		options.UsePathStyle = cfg.ForcePathStyle
	}), nil
}

type configCredentialsProvider struct {
	profile string
}

func (p configCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	var loadOptions []func(*awsconfig.LoadOptions) error
	if p.profile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(p.profile))
	}
	config, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return aws.Credentials{}, err
	}
	return config.Credentials.Retrieve(ctx)
}

func endpointURL(cfg *Config) string {
	if cfg.Endpoint == "" {
		return ""
	}

	if strings.HasPrefix(cfg.Endpoint, "https://") || strings.HasPrefix(cfg.Endpoint, "http://") {
		return cfg.Endpoint
	}
	if cfg.DisableSSL {
		return "http://" + cfg.Endpoint
	}
	return "https://" + cfg.Endpoint
}
