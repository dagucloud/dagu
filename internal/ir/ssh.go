// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// SSHAuth holds the credentials of one SSH hop. Each credential is given
// either as a literal or as a secret registry ref, never both.
type SSHAuth struct {
	// Key is the path to the private key.
	Key string `json:"key,omitempty"`
	// KeySecret is the registry ref of the private key.
	KeySecret string `json:"keySecret,omitempty"`
	// Password is the password.
	Password string `json:"password,omitempty"`
	// PasswordSecret is the registry ref of the password.
	PasswordSecret string `json:"passwordSecret,omitempty"`
	// Passphrase unlocks an encrypted private key.
	Passphrase string `json:"passphrase,omitempty"`
	// PassphraseSecret is the registry ref of the passphrase.
	PassphraseSecret string `json:"passphraseSecret,omitempty"`
}

// HasKey reports whether a private key is set in either form.
func (a SSHAuth) HasKey() bool {
	return a.Key != "" || a.KeySecret != ""
}

// HasPassword reports whether a password is set in either form.
func (a SSHAuth) HasPassword() bool {
	return a.Password != "" || a.PasswordSecret != ""
}

// HasPassphrase reports whether a passphrase is set in either form.
func (a SSHAuth) HasPassphrase() bool {
	return a.Passphrase != "" || a.PassphraseSecret != ""
}

// secretRefs returns the secret refs the hop's auth method uses, named by
// field path. A key uses the key and its passphrase; otherwise a password
// uses the password; otherwise a default key uses the passphrase.
func (a SSHAuth) secretRefs(path string) []secretref.Ref {
	var refs []secretref.Ref
	add := func(field, ref string) {
		if ref != "" {
			refs = append(refs, secretref.Ref{Name: path + "." + field, Ref: ref})
		}
	}
	switch {
	case a.HasKey():
		add("key", a.KeySecret)
		add("passphrase", a.PassphraseSecret)
	case a.HasPassword():
		add("password", a.PasswordSecret)
	default:
		add("passphrase", a.PassphraseSecret)
	}
	return refs
}

// SecretRefs returns the secret refs the connection uses, including those of
// its bastion, named by field path under path.
func (c *SSHConfig) SecretRefs(path string) []secretref.Ref {
	if c == nil {
		return nil
	}
	refs := c.secretRefs(path)
	if c.Bastion != nil {
		refs = append(refs, c.Bastion.secretRefs(path+".bastion")...)
	}
	return refs
}

// ParseSSHAuth parses the key, password, and passphrase fields of the SSH
// hop at path. A key is a path, {file: <path>}, or {secret: <ref>}. A
// password or passphrase is a string or {secret: <ref>}. Errors name the
// field and never include a literal credential.
func ParseSSHAuth(path string, key, password, passphrase any) (SSHAuth, error) {
	var auth SSHAuth
	var errs ErrorList
	var err error
	if auth.Key, auth.KeySecret, err = parseCredential(path+".key", key, true); err != nil {
		errs = append(errs, err)
	}
	if auth.Password, auth.PasswordSecret, err = parseCredential(path+".password", password, false); err != nil {
		errs = append(errs, err)
	}
	if auth.Passphrase, auth.PassphraseSecret, err = parseCredential(path+".passphrase", passphrase, false); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return SSHAuth{}, errs
	}
	return auth, nil
}

const (
	credentialFieldSecret = "secret"
	credentialFieldFile   = "file"
)

// parseCredential returns the literal or the secret ref of a credential.
func parseCredential(field string, raw any, allowFile bool) (string, string, error) {
	var fields map[string]any
	switch v := raw.(type) {
	case nil:
		return "", "", nil
	case string:
		return v, "", nil
	case map[string]any:
		fields = v
	case map[any]any:
		fields = make(map[string]any, len(v))
		for k, val := range v {
			fields[fmt.Sprint(k)] = val
		}
	default:
		return "", "", NewValidationError(field, nil, errors.New(credentialFormHint(allowFile)))
	}

	allowed := []string{credentialFieldSecret}
	if allowFile {
		allowed = append(allowed, credentialFieldFile)
	}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return "", "", NewValidationError(field, nil,
				fmt.Errorf("unknown field %q; use %s", name, strings.Join(allowed, " or ")))
		}
	}

	secret, hasSecret := fields[credentialFieldSecret]
	file, hasFile := fields[credentialFieldFile]
	switch {
	case hasSecret && hasFile:
		return "", "", NewValidationError(field, nil, errors.New("set either secret or file, not both"))
	case hasFile:
		path, ok := file.(string)
		if !ok || path == "" {
			return "", "", NewValidationError(field+".file", nil, errors.New("must be a non-empty path"))
		}
		return path, "", nil
	case hasSecret:
		ref, ok := secret.(string)
		if !ok || ref == "" {
			return "", "", NewValidationError(field+".secret", nil, errors.New("must be a non-empty secret ref"))
		}
		if strings.Contains(ref, "${") {
			return "", "", NewValidationError(field+".secret", ref,
				errors.New("secret ref must be literal; ${...} is not resolved"))
		}
		if err := secretpkg.ValidateRef(ref); err != nil {
			return "", "", NewValidationError(field+".secret", ref, err)
		}
		return "", ref, nil
	default:
		return "", "", NewValidationError(field, nil, errors.New(credentialFormHint(allowFile)))
	}
}

func credentialFormHint(allowFile bool) string {
	if allowFile {
		return "must be a path, {file: <path>}, or {secret: <ref>}"
	}
	return "must be a string or {secret: <ref>}"
}

// clone returns a copy that shares no pointers with c.
func (c *SSHConfig) clone() *SSHConfig {
	cp := *c
	cp.ShellArgs = slices.Clone(c.ShellArgs)
	if c.Bastion != nil {
		bastion := *c.Bastion
		cp.Bastion = &bastion
	}
	return &cp
}
