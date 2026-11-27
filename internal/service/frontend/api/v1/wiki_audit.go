// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// logWikiAudit logs an audit entry for a change to a wiki page.
func (a *API) logWikiAudit(ctx context.Context, action string, details any) {
	a.logAudit(a.wikiAuditContext(ctx), audit.CategoryWiki, action, details)
}

// wikiAuditContext returns ctx with the project whose wiki the wiki
// handlers address, the default project, as the project of the audit
// entries logged with it. It returns ctx as it is while projects are off,
// while audit logging is off, and when ctx already names a project.
func (a *API) wikiAuditContext(ctx context.Context) context.Context {
	if a.projectAccess == nil || !a.isAuditLicensed() {
		return ctx
	}
	if source, ok := audit.SourceContextFromContext(ctx); ok && source.ProjectID != "" {
		return ctx
	}
	// The registry is read as a caller to which roles do not apply: the
	// handler has already checked the user's access.
	access, err := a.projectAccess.Get(ctx, &auth.User{Role: auth.RoleAdmin}, project.DefaultName)
	if err != nil {
		logger.Warn(ctx, "Failed to read the default project for an audit entry", tag.Error(err))
		return ctx
	}
	return withAuditProject(ctx, access.Project)
}

// withAuditProject returns ctx with p as the project of the audit entries
// logged with it.
func withAuditProject(ctx context.Context, p project.Project) context.Context {
	source, ok := audit.SourceContextFromContext(ctx)
	if !ok {
		source = &audit.SourceContext{}
	}
	source.ProjectID, source.ProjectName = p.ID, p.Name
	return audit.WithSourceContext(ctx, source)
}
