// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/workspace/deprecation"
)

var workspaceDeprecationKinds = map[deprecation.Kind]api.WorkspaceDeprecationItemKind{
	deprecation.KindBaseConfig:        api.WorkspaceDeprecationItemKindBaseConfig,
	deprecation.KindSecret:            api.WorkspaceDeprecationItemKindSecret,
	deprecation.KindProfileEntry:      api.WorkspaceDeprecationItemKindProfileEntry,
	deprecation.KindNotificationRoute: api.WorkspaceDeprecationItemKindNotificationRoute,
	deprecation.KindIncidentPolicy:    api.WorkspaceDeprecationItemKindIncidentPolicy,
	deprecation.KindConfigKey:         api.WorkspaceDeprecationItemKindConfigKey,
	deprecation.KindUser:              api.WorkspaceDeprecationItemKindUser,
	deprecation.KindAPIKey:            api.WorkspaceDeprecationItemKindApiKey,
	deprecation.KindWikiDirectory:     api.WorkspaceDeprecationItemKindWikiDirectory,
}

// GetWorkspaceDeprecation lists the workspace settings in use that v3.0
// moves into projects. Requires admin role.
func (a *API) GetWorkspaceDeprecation(ctx context.Context, _ api.GetWorkspaceDeprecationRequestObject) (api.GetWorkspaceDeprecationResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	found, err := deprecation.Find(ctx, a.workspaceDeprecation)
	if err != nil {
		logger.Warn(ctx, "Failed to read some workspace settings", tag.Error(err))
	}
	items := make([]api.WorkspaceDeprecationItem, 0, len(found))
	for _, item := range found {
		items = append(items, api.WorkspaceDeprecationItem{
			Kind: workspaceDeprecationKinds[item.Kind],
			Name: item.Name,
		})
	}
	return api.GetWorkspaceDeprecation200JSONResponse{Items: items}, nil
}
