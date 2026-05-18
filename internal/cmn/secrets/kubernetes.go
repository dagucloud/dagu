// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const defaultKubernetesNamespace = "default"

func init() {
	registerResolver("kubernetes", func(_ []string) Resolver {
		return &kubernetesResolver{}
	})
}

// kubernetesResolver fetches values from Kubernetes Secret resources.
type kubernetesResolver struct {
	client        kubernetesSecretClient // For testing
	clientFactory func(kubernetesClientSettings) (kubernetesSecretClient, error)
	mu            sync.Mutex

	cachedClient   kubernetesSecretClient
	cachedSettings kubernetesClientSettings
}

type kubernetesClientSettings struct {
	kubeconfig string
	context    string
}

type kubernetesSecretRef struct {
	namespace  string
	secretName string
	dataKey    string
}

// Name returns the provider identifier.
func (r *kubernetesResolver) Name() string {
	return "kubernetes"
}

// Validate checks if the secret reference is valid for Kubernetes Secret access.
func (r *kubernetesResolver) Validate(ref core.SecretRef) error {
	_, err := r.parseReference(context.Background(), ref)
	return err
}

// CheckCapability reports that the current Kubernetes check implementation
// must read Secret data to verify the requested data key.
func (r *kubernetesResolver) CheckCapability(core.SecretRef) CheckCapability {
	return CheckCapabilityRequiresValueRead
}

// Resolve fetches the value from a Kubernetes Secret data key.
func (r *kubernetesResolver) Resolve(ctx context.Context, ref core.SecretRef) (string, error) {
	parsed, err := r.parseReference(ctx, ref)
	if err != nil {
		return "", err
	}

	client, err := r.getClient(ctx, ref)
	if err != nil {
		return "", err
	}

	secret, err := client.GetSecret(ctx, parsed.namespace, parsed.secretName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("kubernetes secret %q not found in namespace %q", parsed.secretName, parsed.namespace)
		}
		if apierrors.IsForbidden(err) {
			return "", fmt.Errorf("permission denied reading kubernetes secret %q in namespace %q: %w", parsed.secretName, parsed.namespace, err)
		}
		return "", fmt.Errorf("failed to read kubernetes secret %q in namespace %q: %w", parsed.secretName, parsed.namespace, err)
	}
	if secret == nil {
		return "", fmt.Errorf("kubernetes secret %q not found in namespace %q", parsed.secretName, parsed.namespace)
	}

	value, ok := secret.Data[parsed.dataKey]
	if !ok {
		return "", fmt.Errorf("key %q not found in kubernetes secret %q in namespace %q", parsed.dataKey, parsed.secretName, parsed.namespace)
	}

	return string(value), nil
}

// CheckAccessibility verifies the Kubernetes Secret and data key can be read.
func (r *kubernetesResolver) CheckAccessibility(ctx context.Context, ref core.SecretRef) error {
	_, err := r.Resolve(ctx, ref)
	return err
}

func (r *kubernetesResolver) getClient(ctx context.Context, ref core.SecretRef) (kubernetesSecretClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.client != nil {
		return r.client, nil
	}

	settings := r.resolveClientSettings(ctx, ref)
	if r.cachedClient != nil && r.cachedSettings == settings {
		return r.cachedClient, nil
	}

	client, err := r.newClient(settings)
	if err != nil {
		return nil, err
	}

	r.cachedClient = client
	r.cachedSettings = settings

	return client, nil
}

func (r *kubernetesResolver) resolveClientSettings(ctx context.Context, ref core.SecretRef) kubernetesClientSettings {
	settings := kubernetesClientSettings{}

	cfg := config.GetConfig(ctx)
	if cfg.Secrets.Kubernetes.Kubeconfig != "" {
		settings.kubeconfig = cfg.Secrets.Kubernetes.Kubeconfig
	}
	if cfg.Secrets.Kubernetes.Context != "" {
		settings.context = cfg.Secrets.Kubernetes.Context
	}
	if kubeconfig := strings.TrimSpace(ref.Options["kubeconfig"]); kubeconfig != "" {
		settings.kubeconfig = kubeconfig
	}
	if contextName := strings.TrimSpace(ref.Options["context"]); contextName != "" {
		settings.context = contextName
	}

	return settings
}

func (r *kubernetesResolver) newClient(settings kubernetesClientSettings) (kubernetesSecretClient, error) {
	if r.clientFactory != nil {
		return r.clientFactory(settings)
	}

	restCfg, err := buildKubernetesRESTConfig(settings)
	if err != nil {
		return nil, err
	}

	clientset, err := k8sclient.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &realKubernetesSecretClient{clientset: clientset}, nil
}

func (r *kubernetesResolver) parseReference(ctx context.Context, ref core.SecretRef) (kubernetesSecretRef, error) {
	if strings.TrimSpace(ref.Key) == "" {
		return kubernetesSecretRef{}, fmt.Errorf("key (kubernetes secret reference) is required")
	}

	namespace := r.resolveNamespace(ctx, ref)
	secretName := firstNonEmpty(
		ref.Options["secret_name"],
		ref.Options["name"],
	)
	dataKey := firstNonEmpty(
		ref.Options["secret_key"],
		ref.Options["field"],
	)

	key := strings.Trim(strings.TrimSpace(ref.Key), "/")
	if secretName != "" {
		if dataKey == "" {
			dataKey = key
		}
		return newKubernetesSecretRef(namespace, secretName, dataKey)
	}

	if dataKey != "" {
		return newKubernetesSecretRef(namespace, key, dataKey)
	}

	secretName, dataKey, ok := strings.Cut(key, "/")
	if !ok {
		return kubernetesSecretRef{}, fmt.Errorf("secret name and data key are required; use key as secret-name/data-key or set options.secret_name")
	}

	secretName = strings.TrimSpace(secretName)
	dataKey = strings.Trim(strings.TrimSpace(dataKey), "/")
	if strings.Contains(dataKey, "/") {
		return kubernetesSecretRef{}, fmt.Errorf("key must be secret-name/data-key")
	}

	return newKubernetesSecretRef(namespace, secretName, dataKey)
}

func newKubernetesSecretRef(namespace, secretName, dataKey string) (kubernetesSecretRef, error) {
	ref := kubernetesSecretRef{
		namespace:  namespace,
		secretName: strings.TrimSpace(secretName),
		dataKey:    strings.TrimSpace(dataKey),
	}
	if ref.secretName == "" {
		return kubernetesSecretRef{}, fmt.Errorf("secret name is required")
	}
	if strings.Contains(ref.secretName, "/") {
		return kubernetesSecretRef{}, fmt.Errorf("secret name must not contain '/'")
	}
	if ref.dataKey == "" {
		return kubernetesSecretRef{}, fmt.Errorf("secret data key is required")
	}
	if strings.Contains(ref.dataKey, "/") {
		return kubernetesSecretRef{}, fmt.Errorf("secret data key must not contain '/'")
	}
	return ref, nil
}

func (r *kubernetesResolver) resolveNamespace(ctx context.Context, ref core.SecretRef) string {
	if namespace := strings.TrimSpace(ref.Options["namespace"]); namespace != "" {
		return namespace
	}

	cfg := config.GetConfig(ctx)
	if namespace := strings.TrimSpace(cfg.Secrets.Kubernetes.Namespace); namespace != "" {
		return namespace
	}

	return defaultKubernetesNamespace
}

func buildKubernetesRESTConfig(settings kubernetesClientSettings) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	explicitKubeconfig := settings.kubeconfig != ""
	explicitKubeconfigEnv := hasExplicitKubeconfigEnv()
	if explicitKubeconfig {
		loadingRules.ExplicitPath = settings.kubeconfig
	}

	overrides := &clientcmd.ConfigOverrides{}
	explicitContext := settings.context != ""
	if explicitContext {
		overrides.CurrentContext = settings.context
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := kubeConfig.ClientConfig()
	if err != nil {
		if explicitKubeconfig || explicitKubeconfigEnv || explicitContext || hasAnyKubeconfigFile(loadingRules.GetLoadingPrecedence()) {
			return nil, fmt.Errorf("kubeconfig error: %w", err)
		}

		restCfg, inClusterErr := rest.InClusterConfig()
		if inClusterErr != nil {
			return nil, fmt.Errorf("kubeconfig error: %w; in-cluster error: %w", err, inClusterErr)
		}
		return restCfg, nil
	}
	return restCfg, nil
}

func hasExplicitKubeconfigEnv() bool {
	return strings.TrimSpace(os.Getenv(clientcmd.RecommendedConfigPathEnvVar)) != ""
}

func hasAnyKubeconfigFile(paths []string) bool {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

type kubernetesSecretClient interface {
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
}

type realKubernetesSecretClient struct {
	clientset k8sclient.Interface
}

func (c *realKubernetesSecretClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
