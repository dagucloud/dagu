// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"github.com/dagucloud/dagu/v2/internal/audit"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/dagucloud/dagu/v2/internal/secret"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
	"github.com/dagucloud/dagu/v2/internal/view"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Stores contains the persistence dependencies used by the frontend server.
type Stores struct {
	WorkspaceBaseConfig  dagsettings.BaseConfigProvider
	BaseConfig           dagsettings.BaseConfigStore
	AuthService          *authservice.Service
	UserStore            authmodel.UserStore
	AuthSetupRequired    bool
	RemoteNode           remotenode.Store
	Secret               secret.Store
	Profile              profile.Store
	DAGSettings          dagsettings.Store
	Wiki                 wiki.PageStore
	Notification         notification.Store
	NotificationState    chatbridge.StateStore
	NewNotificationLease func() chatbridge.Lease
	Incident             incident.Store
	IncidentState        chatbridge.StateStore
	NewIncidentLease     func() chatbridge.Lease
	Workspace            workspace.Store
	Upgrade              upgrade.CacheStore
	Audit                audit.Store
	Event                *eventstore.Service
	View                 view.Store
}
