// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/spf13/viper"
)

// OIDCProvisioningPolicyLoader loads the effective login-time OIDC policy.
type OIDCProvisioningPolicyLoader struct {
	configFiles []string
	fallback    OIDCProvisioningPolicy
}

// NewOIDCProvisioningPolicyLoader creates a loader for the configuration files
// that contributed to the startup configuration.
func NewOIDCProvisioningPolicyLoader(
	configFiles []string,
	fallback OIDCProvisioningPolicy,
) *OIDCProvisioningPolicyLoader {
	return &OIDCProvisioningPolicyLoader{
		configFiles: append([]string(nil), configFiles...),
		fallback:    fallback,
	}
}

// Load returns the current effective OIDC provisioning policy.
func (l *OIDCProvisioningPolicyLoader) Load() (OIDCProvisioningPolicy, error) {
	if len(l.configFiles) == 0 {
		return l.fallback, nil
	}

	v := viper.New()
	v.SetConfigType("yaml")
	bindOIDCPolicyEnvironment(v)

	for i, filename := range l.configFiles {
		data, err := os.ReadFile(filename)
		if err != nil {
			return OIDCProvisioningPolicy{}, fmt.Errorf("read OIDC policy config %q: %w", filename, err)
		}
		if i == 0 {
			if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
				return OIDCProvisioningPolicy{}, fmt.Errorf("read OIDC policy config %q: %w", filename, err)
			}
			continue
		}
		if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
			return OIDCProvisioningPolicy{}, fmt.Errorf("merge OIDC policy config %q: %w", filename, err)
		}
	}

	if err := checkForLegacyKeys(v); err != nil {
		return OIDCProvisioningPolicy{}, err
	}

	configLoader := NewConfigLoader(v, WithService(ServiceServer))
	if err := configLoader.loadOIDCWorkspaceMappingsEnv(); err != nil {
		return OIDCProvisioningPolicy{}, err
	}

	var def Definition
	if err := v.Unmarshal(&def); err != nil {
		return OIDCProvisioningPolicy{}, fmt.Errorf("unmarshal OIDC policy config: %w", err)
	}

	cfg := Config{Server: Server{Auth: Auth{OIDC: AuthOIDC{AutoSignup: true}}}}
	if def.Auth != nil {
		configLoader.loadOIDCAuth(&cfg, def.Auth)
	}
	configLoader.setAuthDefaults(&cfg)

	policy := cfg.Server.Auth.OIDC.ProvisioningPolicy()
	if _, err := auth.ParseRole(policy.RoleMapping.DefaultRole); err != nil {
		return OIDCProvisioningPolicy{}, fmt.Errorf("OIDC roleMapping.defaultRole: %w", err)
	}
	if err := validateOIDCWorkspaceMappings(policy.RoleMapping); err != nil {
		return OIDCProvisioningPolicy{}, err
	}
	return policy, nil
}

func bindOIDCPolicyEnvironment(v *viper.Viper) {
	prefix := strings.ToUpper(AppSlug) + "_"
	for _, binding := range envBindings {
		if !strings.HasPrefix(binding.key, "auth.oidc.") {
			continue
		}
		_ = v.BindEnv(binding.key, prefix+binding.env)
	}
}
