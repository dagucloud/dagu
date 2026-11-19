// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const (
	sshFieldKey        = "key"
	sshFieldPassword   = "password"
	sshFieldPassphrase = "passphrase"
	sshFieldBastion    = "bastion"
)

// collectCredentialRefs records the secret refs that the DAG's SSH
// connections use: the DAG-level ssh and every ssh or sftp step, including
// foreach bodies and handlers. Each ref is listed once, under the first field
// that names it.
func (s *dagBuildState) collectCredentialRefs() {
	refs := s.result.SSH.SecretRefs("ssh")
	walkSteps(s.result, func(step ir.Step, path string) {
		switch step.ExecutorConfig.Type {
		case "ssh", "sftp":
		default:
			return
		}
		cfg, err := stepSSHConfig(path+".with", step.ExecutorConfig.Config)
		if err != nil {
			s.errs = append(s.errs, err)
			return
		}
		refs = append(refs, cfg.SecretRefs(path+".with")...)
	})

	var unique []secretref.Ref
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, ok := seen[ref.Ref]; ok {
			continue
		}
		seen[ref.Ref] = struct{}{}
		unique = append(unique, ref)
	}
	s.result.CredentialRefs = unique
}

// stepSSHConfig parses the credentials of an ssh or sftp step's with block.
func stepSSHConfig(path string, cfg map[string]any) (*ir.SSHConfig, error) {
	auth, err := ir.ParseSSHAuth(path, cfg[sshFieldKey], cfg[sshFieldPassword], cfg[sshFieldPassphrase])
	if err != nil {
		return nil, err
	}
	result := &ir.SSHConfig{SSHAuth: auth}
	if bastion, ok := cfg[sshFieldBastion].(map[string]any); ok {
		auth, err := ir.ParseSSHAuth(path+".bastion",
			bastion[sshFieldKey], bastion[sshFieldPassword], bastion[sshFieldPassphrase])
		if err != nil {
			return nil, err
		}
		result.Bastion = &ir.BastionConfig{SSHAuth: auth}
	}
	return result, nil
}

// inheritSSHAuth sets the effective SSH credentials of a DAG built on a base.
// The field-by-field merge would mix a credential's forms across layers.
func inheritSSHAuth(effective, base, current *ir.DAG) {
	if effective.SSH == nil || base.SSH == nil || current.SSH == nil {
		return
	}
	effective.SSH.SSHAuth = mergeSSHAuth(base.SSH.SSHAuth, current.SSH.SSHAuth)
	if effective.SSH.Bastion != nil && base.SSH.Bastion != nil && current.SSH.Bastion != nil {
		effective.SSH.Bastion.SSHAuth = mergeSSHAuth(base.SSH.Bastion.SSHAuth, current.SSH.Bastion.SSHAuth)
	}
}

// mergeSSHAuth applies the credentials of an override layer to inherited
// ones. A credential set in the override replaces the inherited one whatever
// its form. A key also replaces the passphrase, which belongs to the key it
// unlocks.
func mergeSSHAuth(base, override ir.SSHAuth) ir.SSHAuth {
	merged := base
	if override.HasKey() || override.HasPassphrase() {
		merged.Passphrase, merged.PassphraseSecret = override.Passphrase, override.PassphraseSecret
	}
	if override.HasKey() {
		merged.Key, merged.KeySecret = override.Key, override.KeySecret
	}
	if override.HasPassword() {
		merged.Password, merged.PasswordSecret = override.Password, override.PasswordSecret
	}
	return merged
}

// mergeSSHMaps merges the raw ssh maps of two base config layers with the
// rules of mergeSSHAuth: values replace inherited ones whole, and a key drops
// the inherited passphrase.
func mergeSSHMaps(base, override map[string]any) map[string]any {
	merged := cloneMap(base)
	if _, ok := override[sshFieldKey]; ok {
		delete(merged, sshFieldPassphrase)
	}
	for key, value := range override {
		baseBastion, baseIsMap := merged[key].(map[string]any)
		overrideBastion, overrideIsMap := value.(map[string]any)
		if key == sshFieldBastion && baseIsMap && overrideIsMap {
			merged[key] = mergeSSHMaps(baseBastion, overrideBastion)
			continue
		}
		merged[key] = cloneAny(value)
	}
	return merged
}
