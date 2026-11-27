// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"sync/atomic"

	"github.com/dagucloud/dagu/v2/internal/audit"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/dagpin"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
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
	Calendar             persis.CalendarStore
	AuthService          *authservice.Service
	UserStore            authmodel.UserStore
	AuthSetupRequired    bool
	RemoteNode           remotenode.Store
	Secret               secret.Store
	Profile              profile.Store
	DAGSettings          dagsettings.Store
	DAGPins              dagpin.Store
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
	Inventory            inventory.Store
	HostKeys             inventory.HostKeyStore
	WorkerNodes          inventory.WorkerNodeStore
	PublicWebhooks       publicwebhook.Store
	// Projects is the project registry. It is nil while projects are off.
	Projects project.Registry
	// Connections keeps OAuth apps and connections. It is nil without
	// encrypted storage, as Connect and ConnectEvents are.
	Connections connection.Store
	// Connect runs connect sessions.
	Connect *connect.Engine
	// ConnectEvents receives what Connect reports.
	ConnectEvents *ConnectEvents
}

// ConnectEvents passes the reports of a connect engine, which the stores
// build before the API exists, to the handler that the server sets. A
// report that comes before the handler is set is dropped.
type ConnectEvents struct {
	revoked atomic.Pointer[func(context.Context, connect.Revocation)]
}

// Revoked passes r to the handler that HandleRevoked set.
func (e *ConnectEvents) Revoked(ctx context.Context, r connect.Revocation) {
	if fn := e.revoked.Load(); fn != nil {
		(*fn)(ctx, r)
	}
}

// HandleRevoked sets the handler of Revoked.
func (e *ConnectEvents) HandleRevoked(fn func(context.Context, connect.Revocation)) {
	e.revoked.Store(&fn)
}
