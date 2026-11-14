// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package deprecation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace/deprecation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind(t *testing.T) {
	dagsDir := t.TempDir()
	writeFile(t, filepath.Join(dagsDir, "workspaces", "billing", "base.yaml"), "env:\n  - REGION: eu\n")
	writeFile(t, filepath.Join(dagsDir, "workspaces", "empty", "base.yaml"), "\n")
	wikiDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wikiDir, "billing", "invoices"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(wikiDir, "billing", "ledger"), 0o750))
	billingProfile, err := profile.WorkspaceInheritedRef("billing")
	require.NoError(t, err)

	items, err := deprecation.Find(context.Background(), deprecation.Sources{
		DAGsDir:    dagsDir,
		WikiDir:    wikiDir,
		ConfigKeys: []string{"auth.oidc.role_mapping.workspace_mappings"},
		DAGs: dagLister{
			dag("invoices", "workspace=billing"),
			// Its wiki directory does not exist.
			dag("payroll", "workspace=billing"),
			// Without a workspace label, billing/ledger is an ordinary folder.
			dag("ledger"),
		},
		Accounts: accountLister{
			users: []*auth.User{
				{Username: "alice", WorkspaceAccess: &auth.WorkspaceAccess{All: false}},
				{Username: "bob"},
			},
			keys: []*auth.APIKey{
				{Name: "ci", WorkspaceAccess: &auth.WorkspaceAccess{All: false}},
				{Name: "ops", WorkspaceAccess: auth.AllWorkspaceAccess()},
			},
		},
		Secrets: secretLister{
			{Workspace: "billing", Ref: "DB_PASSWORD"},
			{Workspace: secret.GlobalWorkspace, Ref: "SHARED_TOKEN"},
		},
		Profiles: profileLister{
			{Name: billingProfile.StorageName(), Entries: []profile.Entry{{Key: "API_URL"}}},
			{Name: "_global", Entries: []profile.Entry{{Key: "GLOBAL_URL"}}},
		},
		NotificationRoutes: routeSetLister{
			{Scope: notification.RouteScopeWorkspace, Workspace: "billing"},
			{Scope: notification.RouteScopeWorkspace, Workspace: "ops", InheritGlobal: true},
			{Scope: notification.RouteScopeGlobal, InheritGlobal: false},
		},
		IncidentPolicies: policySetLister{
			{Scope: incident.PolicyScopeWorkspace, Workspace: "billing"},
			{Scope: incident.PolicyScopeWorkspace, Workspace: "ops", InheritParent: true},
			{Scope: incident.PolicyScopeDAG, DAGName: "invoices"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []deprecation.Item{
		{Kind: deprecation.KindBaseConfig, Name: "billing", Path: filepath.Join(dagsDir, "workspaces", "billing", "base.yaml")},
		{Kind: deprecation.KindSecret, Name: "DB_PASSWORD (workspace billing)"},
		{Kind: deprecation.KindProfileEntry, Name: "API_URL (workspace billing)"},
		{Kind: deprecation.KindNotificationRoute, Name: "workspace billing"},
		{Kind: deprecation.KindIncidentPolicy, Name: "workspace billing"},
		{Kind: deprecation.KindConfigKey, Name: "auth.oidc.role_mapping.workspace_mappings"},
		{Kind: deprecation.KindUser, Name: "alice"},
		{Kind: deprecation.KindAPIKey, Name: "ci"},
		{Kind: deprecation.KindWikiDirectory, Name: "billing/invoices", Path: filepath.Join(wikiDir, "billing", "invoices")},
	}, items)
}

func TestFindWithoutWorkspaceSettings(t *testing.T) {
	items, err := deprecation.Find(context.Background(), deprecation.Sources{
		DAGsDir:  t.TempDir(),
		WikiDir:  t.TempDir(),
		DAGs:     dagLister{dag("ledger")},
		Accounts: accountLister{users: []*auth.User{{Username: "bob"}}},
		Secrets:  secretLister{{Workspace: secret.GlobalWorkspace, Ref: "SHARED_TOKEN"}},
	})

	require.NoError(t, err)
	assert.Empty(t, items)
}

// A failing source must not hide what the other sources hold.
func TestFindReportsFailedSource(t *testing.T) {
	items, err := deprecation.Find(context.Background(), deprecation.Sources{
		ConfigKeys: []string{"auth.proxy.role_mapping.default_workspace_access"},
		Secrets:    failingSecretLister{},
	})

	require.ErrorContains(t, err, "list secrets")
	assert.Equal(t, []deprecation.Item{
		{Kind: deprecation.KindConfigKey, Name: "auth.proxy.role_mapping.default_workspace_access"},
	}, items)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func dag(name string, labels ...string) persis.DAGListItem {
	return persis.DAGListItem{ID: name, DAG: &ir.DAG{Name: name, Labels: ir.NewLabels(labels)}}
}

type dagLister []persis.DAGListItem

func (l dagLister) ListIncludingSearchPaths(_ context.Context, opts persis.DAGListOptions) (pagination.PaginatedResult[persis.DAGListItem], []string, error) {
	return pagination.NewPaginatedResult([]persis.DAGListItem(l), len(l), *opts.Paginator), nil, nil
}

type accountLister struct {
	users []*auth.User
	keys  []*auth.APIKey
}

func (l accountLister) ListUsers(context.Context) ([]*auth.User, error) {
	return l.users, nil
}

func (l accountLister) ListAPIKeys(context.Context) ([]*auth.APIKey, error) {
	return l.keys, nil
}

type secretLister []*secret.Secret

func (l secretLister) List(context.Context, secret.ListOptions) ([]*secret.Secret, error) {
	return l, nil
}

type failingSecretLister struct{}

func (failingSecretLister) List(context.Context, secret.ListOptions) ([]*secret.Secret, error) {
	return nil, errors.New("store unavailable")
}

type profileLister []*profile.Profile

func (l profileLister) List(context.Context) ([]*profile.Profile, error) {
	return l, nil
}

type routeSetLister []*notification.RouteSet

func (l routeSetLister) ListRouteSets(context.Context) ([]*notification.RouteSet, error) {
	return l, nil
}

type policySetLister []*incident.PolicySet

func (l policySetLister) ListPolicySets(context.Context) ([]*incident.PolicySet, error) {
	return l, nil
}
