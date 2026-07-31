// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesResolver_Validate(t *testing.T) {
	t.Parallel()

	resolver := &kubernetesResolver{}

	tests := []struct {
		name        string
		ref         core.SecretRef
		errContains string
	}{
		{
			name: "SecretNameSlashDataKey",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "app-secrets/db-password",
			},
		},
		{
			name: "SecretNameOption",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "db-password",
				Options:  map[string]string{"secret_name": "app-secrets"},
			},
		},
		{
			name: "FieldOption",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "app-secrets",
				Options:  map[string]string{"field": "db-password"},
			},
		},
		{
			name: "MissingKey",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
			},
			errContains: "key (kubernetes secret reference) is required",
		},
		{
			name: "NoSecretName",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "db-password",
			},
			errContains: "secret name and data key are required",
		},
		{
			name: "TooManyPathSegments",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "team/app-secrets/db-password",
			},
			errContains: "key must be secret-name/data-key",
		},
		{
			name: "MissingDataKey",
			ref: core.SecretRef{
				Name:     "DB_PASSWORD",
				Provider: "kubernetes",
				Key:      "/",
				Options:  map[string]string{"secret_name": "app-secrets"},
			},
			errContains: "secret data key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := resolver.Validate(tt.ref)
			if tt.errContains == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestKubernetesResolver_Resolve(t *testing.T) {
	t.Parallel()

	client := &mockKubernetesSecretClient{
		secrets: map[string]*corev1.Secret{
			"prod/app-secrets": {
				ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "prod"},
				Data: map[string][]byte{
					"db-password": []byte("secret-password"),
					"api-token":   []byte("secret-token"),
				},
			},
		},
	}
	resolver := &kubernetesResolver{client: client}

	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{
			Kubernetes: config.KubernetesSecretsConfig{Namespace: "prod"},
		},
	})

	value, err := resolver.Resolve(ctx, core.SecretRef{
		Name:     "DB_PASSWORD",
		Provider: "kubernetes",
		Key:      "app-secrets/db-password",
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-password", value)
	assert.Equal(t, []mockKubernetesSecretCall{{namespace: "prod", name: "app-secrets"}}, client.calls)
}

func TestKubernetesResolver_ResolveWithOptions(t *testing.T) {
	t.Parallel()

	client := &mockKubernetesSecretClient{
		secrets: map[string]*corev1.Secret{
			"staging/app-secrets": {
				ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "staging"},
				Data:       map[string][]byte{"api-token": []byte("secret-token")},
			},
		},
	}
	resolver := &kubernetesResolver{client: client}

	value, err := resolver.Resolve(context.Background(), core.SecretRef{
		Name:     "API_TOKEN",
		Provider: "kubernetes",
		Key:      "api-token",
		Options: map[string]string{
			"secret_name": "app-secrets",
			"namespace":   "staging",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-token", value)
	assert.Equal(t, []mockKubernetesSecretCall{{namespace: "staging", name: "app-secrets"}}, client.calls)
}

func TestKubernetesResolver_ResolveErrors(t *testing.T) {
	t.Parallel()

	t.Run("SecretNotFound", func(t *testing.T) {
		t.Parallel()

		resolver := &kubernetesResolver{client: &mockKubernetesSecretClient{}}
		_, err := resolver.Resolve(context.Background(), core.SecretRef{
			Name:     "DB_PASSWORD",
			Provider: "kubernetes",
			Key:      "app-secrets/db-password",
			Options:  map[string]string{"namespace": "prod"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `kubernetes secret "app-secrets" not found in namespace "prod"`)
	})

	t.Run("DataKeyNotFound", func(t *testing.T) {
		t.Parallel()

		resolver := &kubernetesResolver{client: &mockKubernetesSecretClient{
			secrets: map[string]*corev1.Secret{
				"default/app-secrets": {
					ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "default"},
					Data:       map[string][]byte{"api-token": []byte("secret-token")},
				},
			},
		}}

		_, err := resolver.Resolve(context.Background(), core.SecretRef{
			Name:     "DB_PASSWORD",
			Provider: "kubernetes",
			Key:      "app-secrets/db-password",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `key "db-password" not found`)
		assert.NotContains(t, err.Error(), "api-token")
	})

	t.Run("Forbidden", func(t *testing.T) {
		t.Parallel()

		resolver := &kubernetesResolver{client: &mockKubernetesSecretClient{
			err: apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "app-secrets", errors.New("denied")),
		}}

		_, err := resolver.Resolve(context.Background(), core.SecretRef{
			Name:     "DB_PASSWORD",
			Provider: "kubernetes",
			Key:      "app-secrets/db-password",
			Options:  map[string]string{"namespace": "prod"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied reading kubernetes secret")
	})
}

func TestKubernetesResolver_ResolveClientSettings(t *testing.T) {
	t.Parallel()

	resolver := &kubernetesResolver{}
	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{
			Kubernetes: config.KubernetesSecretsConfig{
				Kubeconfig: "/global/kubeconfig",
				Context:    "global-context",
			},
		},
	})

	settings := resolver.resolveClientSettings(ctx, core.SecretRef{
		Options: map[string]string{
			"kubeconfig": "/override/kubeconfig",
			"context":    "override-context",
		},
	})

	assert.Equal(t, kubernetesClientSettings{
		kubeconfig: "/override/kubeconfig",
		context:    "override-context",
	}, settings)
}

func TestKubernetesResolver_ClientCache(t *testing.T) {
	t.Parallel()

	var calls int
	resolver := &kubernetesResolver{
		clientFactory: func(_ kubernetesClientSettings) (kubernetesSecretClient, error) {
			calls++
			return &mockKubernetesSecretClient{}, nil
		},
	}

	ctx := config.WithConfig(context.Background(), &config.Config{
		Secrets: config.SecretsConfig{
			Kubernetes: config.KubernetesSecretsConfig{
				Kubeconfig: "/global/kubeconfig",
				Context:    "global-context",
			},
		},
	})
	ref := core.SecretRef{
		Name:     "DB_PASSWORD",
		Provider: "kubernetes",
		Key:      "app-secrets/db-password",
	}

	_, err := resolver.getClient(ctx, ref)
	require.NoError(t, err)
	_, err = resolver.getClient(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

type mockKubernetesSecretClient struct {
	secrets map[string]*corev1.Secret
	err     error
	calls   []mockKubernetesSecretCall
}

type mockKubernetesSecretCall struct {
	namespace string
	name      string
}

func (c *mockKubernetesSecretClient) GetSecret(_ context.Context, namespace, name string) (*corev1.Secret, error) {
	c.calls = append(c.calls, mockKubernetesSecretCall{namespace: namespace, name: name})
	if c.err != nil {
		return nil, c.err
	}
	if c.secrets == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	secret := c.secrets[namespace+"/"+name]
	if secret == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	return secret, nil
}
