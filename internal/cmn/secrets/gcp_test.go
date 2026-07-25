// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"hash/crc32"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type gcpSecretManagerTestClient struct {
	accessSecretVersion func(context.Context, *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error)
	close               func() error
}

func (c *gcpSecretManagerTestClient) AccessSecretVersion(ctx context.Context, request *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	return c.accessSecretVersion(ctx, request)
}

func (c *gcpSecretManagerTestClient) Close() error {
	if c.close != nil {
		return c.close()
	}
	return nil
}

func TestGCPSecretManagerResolverValidate(t *testing.T) {
	resolver := &gcpSecretManagerResolver{}
	tests := []struct {
		name    string
		ref     core.SecretRef
		wantErr string
	}{
		{name: "ShortID", ref: core.SecretRef{Key: "database-password"}},
		{name: "GlobalSecret", ref: core.SecretRef{Key: "projects/project-a/secrets/database-password"}},
		{name: "GlobalVersion", ref: core.SecretRef{Key: "projects/project-a/secrets/database-password/versions/5"}},
		{name: "RegionalSecret", ref: core.SecretRef{Key: "projects/project-a/locations/us-central1/secrets/database-password"}},
		{name: "RegionalVersion", ref: core.SecretRef{Key: "projects/project-a/locations/us-central1/secrets/database-password/versions/latest"}},
		{name: "Empty", ref: core.SecretRef{}, wantErr: "key"},
		{name: "ShortIDWithSlash", ref: core.SecretRef{Key: "team/database-password"}, wantErr: "must not contain slashes"},
		{name: "MalformedResource", ref: core.SecretRef{Key: "projects/project-a/locations/us-central1/secrets"}, wantErr: "invalid"},
		{name: "VersionConflict", ref: core.SecretRef{Key: "projects/project-a/secrets/database-password/versions/5", Options: map[string]string{"version": "6"}}, wantErr: "conflicts"},
		{name: "ResourceWhitespaceOptions", ref: core.SecretRef{Key: "projects/project-a/secrets/database-password/versions/5", Options: map[string]string{"project_id": " ", "location": " ", "version": " "}}},
		{name: "ProjectConflict", ref: core.SecretRef{Key: "projects/project-a/secrets/database-password", Options: map[string]string{"project_id": "project-b"}}, wantErr: "cannot be used"},
		{name: "InvalidLocation", ref: core.SecretRef{Key: "database-password", Options: map[string]string{"location": "evil.example.com:443"}}, wantErr: "invalid characters"},
		{name: "UnsupportedOption", ref: core.SecretRef{Key: "database-password", Options: map[string]string{"profile": "production"}}, wantErr: `unsupported option "profile"`},
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

func TestGCPSecretManagerResolverRegistered(t *testing.T) {
	resolver := NewRegistry().Get(gcpSecretManagerProvider)
	require.NotNil(t, resolver)
	assert.Equal(t, gcpSecretManagerProvider, resolver.Name())
}

func TestGCPSecretManagerResolverResolve(t *testing.T) {
	var locations []string
	var resources []string
	data := []byte(`{"token":"resolved","enabled":true}`)
	checksum := int64(crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)))
	resolver := &gcpSecretManagerResolver{
		clientFactory: func(_ context.Context, location string) (gcpSecretManagerClient, error) {
			locations = append(locations, location)
			return &gcpSecretManagerTestClient{accessSecretVersion: func(_ context.Context, request *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
				resources = append(resources, request.Name)
				return &secretmanagerpb.AccessSecretVersionResponse{
					Payload: &secretmanagerpb.SecretPayload{Data: data, DataCrc32C: &checksum},
				}, nil
			}}, nil
		},
	}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{GCP: config.GCPSecretsConfig{ProjectID: " config-project "}},
	})

	got, err := resolver.Resolve(ctx, core.SecretRef{Key: "database-password", Options: map[string]string{"field": "token"}})
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)

	got, err = resolver.Resolve(ctx, core.SecretRef{
		Key: "database-password",
		Options: map[string]string{
			"project_id": " option-project ",
			"location":   " us-central1 ",
			"version":    " 5 ",
			"field":      "enabled",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", got)

	got, err = resolver.Resolve(context.Background(), core.SecretRef{Key: "projects/full-project/secrets/name/versions/7"})
	require.NoError(t, err)
	assert.Equal(t, string(data), got)

	got, err = resolver.Resolve(context.Background(), core.SecretRef{
		Key:     "projects/full-project/locations/europe-west1/secrets/name",
		Options: map[string]string{"version": " 9 "},
	})
	require.NoError(t, err)
	assert.Equal(t, string(data), got)

	assert.Equal(t, []string{"", "us-central1", "europe-west1"}, locations)
	assert.Equal(t, []string{
		"projects/config-project/secrets/database-password/versions/latest",
		"projects/option-project/locations/us-central1/secrets/database-password/versions/5",
		"projects/full-project/secrets/name/versions/7",
		"projects/full-project/locations/europe-west1/secrets/name/versions/9",
	}, resources)
}

func TestGCPSecretManagerResolverErrors(t *testing.T) {
	t.Run("MissingProject", func(t *testing.T) {
		resolver := &gcpSecretManagerResolver{}
		_, err := resolver.Resolve(context.Background(), core.SecretRef{Key: "name"})
		require.ErrorContains(t, err, "project ID is required")
	})

	badChecksum := int64(1)
	tests := []struct {
		name     string
		response *secretmanagerpb.AccessSecretVersionResponse
		err      error
		wantErr  string
	}{
		{name: "ReadError", err: fmt.Errorf("permission denied"), wantErr: "failed to read"},
		{name: "NilResponse", wantErr: "has no payload"},
		{name: "MissingPayload", response: &secretmanagerpb.AccessSecretVersionResponse{}, wantErr: "has no payload"},
		{name: "ChecksumMismatch", response: &secretmanagerpb.AccessSecretVersionResponse{Payload: &secretmanagerpb.SecretPayload{Data: []byte("value"), DataCrc32C: &badChecksum}}, wantErr: "failed CRC32C"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &gcpSecretManagerResolver{
				clientFactory: func(context.Context, string) (gcpSecretManagerClient, error) {
					return &gcpSecretManagerTestClient{accessSecretVersion: func(context.Context, *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
						return tc.response, tc.err
					}}, nil
				},
			}
			_, err := resolver.Resolve(context.Background(), core.SecretRef{Key: "projects/project-a/secrets/name/versions/latest"})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestGCPSecretManagerResolverClose(t *testing.T) {
	var closed int
	value := []byte("secret")
	resolver := &gcpSecretManagerResolver{
		clientFactory: func(context.Context, string) (gcpSecretManagerClient, error) {
			return &gcpSecretManagerTestClient{
				accessSecretVersion: func(context.Context, *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
					return &secretmanagerpb.AccessSecretVersionResponse{Payload: &secretmanagerpb.SecretPayload{Data: value}}, nil
				},
				close: func() error {
					closed++
					return nil
				},
			}, nil
		},
	}

	for _, location := range []string{"", "us-central1"} {
		_, err := resolver.Resolve(context.Background(), core.SecretRef{
			Key: "name", Options: map[string]string{"project_id": "project-a", "location": location},
		})
		require.NoError(t, err)
	}
	require.NoError(t, resolver.Close())
	assert.Equal(t, 2, closed)
}
