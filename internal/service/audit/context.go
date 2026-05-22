// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"

	"github.com/dagucloud/dagu/internal/auth"
)

type sourceContextKey struct{}

// SourceContext carries request attribution and correlation metadata across
// transport handlers and downstream domain audit hooks.
type SourceContext struct {
	Source                    string
	Surface                   string
	RequestID                 string
	CorrelationID             string
	SessionID                 string
	ClientName                string
	ClientVersion             string
	Transport                 string
	CredentialID              string
	CredentialName            string
	CredentialType            string
	CredentialAllowedSurfaces []string
	AttributionClass          string
	CredentialOwnerID         string
	ServiceAccountID          string
	RequestedWorkspace        string
	ResolvedWorkspace         string
	SubjectType               string
	SubjectID                 string
	SubjectName               string
	MCPTool                   string
	MCPAction                 string
}

// WithSourceContext stores a copy of source context on ctx.
func WithSourceContext(ctx context.Context, source *SourceContext) context.Context {
	if source == nil {
		return ctx
	}
	copy := *source
	copy.CredentialAllowedSurfaces = append([]string(nil), source.CredentialAllowedSurfaces...)
	return context.WithValue(ctx, sourceContextKey{}, &copy)
}

// SourceContextFromContext returns source context if present.
func SourceContextFromContext(ctx context.Context) (*SourceContext, bool) {
	source, ok := ctx.Value(sourceContextKey{}).(*SourceContext)
	if !ok || source == nil {
		return nil, false
	}
	copy := *source
	copy.CredentialAllowedSurfaces = append([]string(nil), source.CredentialAllowedSurfaces...)
	return &copy, true
}

// ApplySourceContext copies searchable source fields onto an entry.
func ApplySourceContext(entry *Entry, source *SourceContext) {
	if entry == nil || source == nil {
		return
	}
	if entry.Source == "" {
		entry.Source = source.Source
	}
	if entry.Surface == "" {
		entry.Surface = source.Surface
	}
	if entry.CorrelationID == "" {
		entry.CorrelationID = source.CorrelationID
	}
	if entry.Workspace == "" {
		entry.Workspace = source.ResolvedWorkspace
	}
	if entry.CredentialID == "" {
		entry.CredentialID = source.CredentialID
	}
	if entry.CredentialType == "" {
		entry.CredentialType = source.CredentialType
	}
	if entry.MCPTool == "" {
		entry.MCPTool = source.MCPTool
	}
}

// ApplyAPIKeyCredential copies API-key attribution metadata onto a source context.
func ApplyAPIKeyCredential(source *SourceContext, apiKey *auth.APIKey) {
	if source == nil || apiKey == nil {
		return
	}
	apiKey = auth.NormalizeAPIKeyMetadata(apiKey)
	source.CredentialID = apiKey.ID
	source.CredentialName = apiKey.Name
	source.CredentialType = "api_key"
	source.CredentialAllowedSurfaces = auth.APIKeySurfaceStrings(apiKey.AllowedSurfaces)
	source.AttributionClass = string(apiKey.AttributionClass)
	switch apiKey.AttributionClass {
	case auth.APIKeyAttributionUserOwned:
		source.SubjectType = "user"
		source.SubjectID = apiKey.OwnerUserID
		source.SubjectName = apiKey.OwnerUsername
		source.CredentialOwnerID = apiKey.OwnerUserID
	case auth.APIKeyAttributionServiceAccount:
		source.SubjectType = "service_account"
		source.SubjectID = apiKey.ServiceAccountID
		source.SubjectName = apiKey.ServiceAccountName
		source.ServiceAccountID = apiKey.ServiceAccountID
	}
}
