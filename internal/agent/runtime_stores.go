// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/secret"
)

// RuntimeStores contains the stores and resolvers used by runtime agent flows.
type RuntimeStores struct {
	ConfigStore     ConfigStore
	ModelStore      ModelStore
	MemoryStore     MemoryStore
	SoulStore       SoulStore
	OAuthManager    *agentoauth.Manager
	ContextResolver RemoteContextResolver
	SecretStore     secret.Store
	ProfileStore    profile.Store
	ReferencesDir   string
}
