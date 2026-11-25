// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import "context"

type cloudCredentialsKey struct{}

// WithCloudCredentials returns a context from which ContextCloudCredentials
// reads this server's Dagu Cloud credentials through get.
func WithCloudCredentials(ctx context.Context, get func() (CloudCredentials, bool)) context.Context {
	return context.WithValue(ctx, cloudCredentialsKey{}, get)
}

// ContextCloudCredentials returns the Dagu Cloud credentials ctx carries. It
// reports false when ctx carries none or the server is not connected.
func ContextCloudCredentials(ctx context.Context) (CloudCredentials, bool) {
	get, _ := ctx.Value(cloudCredentialsKey{}).(func() (CloudCredentials, bool))
	if get == nil {
		return CloudCredentials{}, false
	}
	return get()
}
