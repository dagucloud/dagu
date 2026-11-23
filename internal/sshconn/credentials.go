// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sshconn

import (
	"context"
	"fmt"
)

type runCredentialsCtxKey struct{}

// runCredentials holds what one run's SSH connections share: the values of
// the secret refs resolved at run start, and the keys already parsed.
type runCredentials struct {
	values  map[string]string
	signers signerCache
}

// WithSecretValues returns a context for one run whose SSH connections read
// credentials given as secret refs from values, keyed by ref. Connections
// made under it also share parsed private keys, so an encrypted key is
// decrypted once per run rather than once per step or foreach item.
func WithSecretValues(ctx context.Context, values map[string]string) context.Context {
	return context.WithValue(ctx, runCredentialsCtxKey{}, &runCredentials{values: values})
}

func credentialsFromContext(ctx context.Context) *runCredentials {
	creds, _ := ctx.Value(runCredentialsCtxKey{}).(*runCredentials)
	return creds
}

// secretValue returns the value of the secret ref that the credential at
// field names.
func secretValue(ctx context.Context, field, ref string) (string, error) {
	var values map[string]string
	if creds := credentialsFromContext(ctx); creds != nil {
		values = creds.values
	}
	value, ok := values[ref]
	if !ok {
		return "", fmt.Errorf("%s: secret %q was not resolved at run start", field, ref)
	}
	return value, nil
}

// credentialValue returns a credential given as a literal or a secret ref.
func credentialValue(ctx context.Context, field, literal, ref string) (string, error) {
	if ref == "" {
		return literal, nil
	}
	return secretValue(ctx, field, ref)
}
