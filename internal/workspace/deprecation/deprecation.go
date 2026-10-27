// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package deprecation finds the workspace settings in use that v3.0 moves
// into projects.
package deprecation

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Warning is the message logged with the settings Find returns.
const Warning = "Workspaces become projects in v3.0, and a migration command moves these settings"

// Kind is a kind of workspace setting.
type Kind string

// Kinds, in the order Find returns them.
const (
	KindBaseConfig        Kind = "base config"
	KindSecret            Kind = "secret"
	KindProfileEntry      Kind = "profile entry"
	KindNotificationRoute Kind = "notification route"
	KindIncidentPolicy    Kind = "incident policy"
	KindConfigKey         Kind = "config key"
	KindUser              Kind = "user"
	KindAPIKey            Kind = "API key"
	KindWikiDirectory     Kind = "wiki directory"
)

var kindOrder = []Kind{
	KindBaseConfig,
	KindSecret,
	KindProfileEntry,
	KindNotificationRoute,
	KindIncidentPolicy,
	KindConfigKey,
	KindUser,
	KindAPIKey,
	KindWikiDirectory,
}

// Item is one workspace setting in use.
type Item struct {
	Kind Kind
	// Name identifies the setting, such as a workspace, a secret and its
	// workspace, or a username.
	Name string
	// Path is the file or directory that holds the setting, if any.
	Path string
}

// String returns the kind followed by the path, or by the name when the
// setting has no path.
func (i Item) String() string {
	return string(i.Kind) + " " + cmp.Or(i.Path, i.Name)
}

// Sources are where Find reads workspace settings. Find skips an empty
// directory and a nil source.
type Sources struct {
	// DAGsDir holds workspace base configs.
	DAGsDir string
	// WikiDir holds the per-DAG wiki directories of workspace DAGs.
	WikiDir string
	// ConfigKeys are the workspace access keys the configuration sets.
	ConfigKeys []string

	DAGs               DAGLister
	Accounts           AccountLister
	Secrets            SecretLister
	Profiles           ProfileLister
	NotificationRoutes RouteSetLister
	IncidentPolicies   PolicySetLister
}

// DAGLister lists DAG definitions.
type DAGLister interface {
	ListIncludingSearchPaths(ctx context.Context, opts persis.DAGListOptions) (pagination.PaginatedResult[persis.DAGListItem], []string, error)
}

// AccountLister lists users and API keys.
type AccountLister interface {
	ListUsers(ctx context.Context) ([]*auth.User, error)
	ListAPIKeys(ctx context.Context) ([]*auth.APIKey, error)
}

// SecretLister lists registered secrets.
type SecretLister interface {
	List(ctx context.Context, opts secret.ListOptions) ([]*secret.Secret, error)
}

// ProfileLister lists runtime profiles, inherited layers included.
type ProfileLister interface {
	List(ctx context.Context) ([]*profile.Profile, error)
}

// RouteSetLister lists notification route sets.
type RouteSetLister interface {
	ListRouteSets(ctx context.Context) ([]*notification.RouteSet, error)
}

// PolicySetLister lists incident policy sets.
type PolicySetLister interface {
	ListPolicySets(ctx context.Context) ([]*incident.PolicySet, error)
}

// Find returns the workspace settings in use, ordered by kind and name. A
// source that cannot be read is reported in the returned error while the
// settings of every other source are still returned.
func Find(ctx context.Context, src Sources) ([]Item, error) {
	var (
		items []Item
		errs  []error
	)
	collect := func(found []Item, err error) {
		items = append(items, found...)
		if err != nil {
			errs = append(errs, err)
		}
	}

	collect(baseConfigs(src.DAGsDir))
	if src.Secrets != nil {
		collect(secrets(ctx, src.Secrets))
	}
	if src.Profiles != nil {
		collect(profileEntries(ctx, src.Profiles))
	}
	if src.NotificationRoutes != nil {
		collect(notificationRoutes(ctx, src.NotificationRoutes))
	}
	if src.IncidentPolicies != nil {
		collect(incidentPolicies(ctx, src.IncidentPolicies))
	}
	for _, key := range src.ConfigKeys {
		items = append(items, Item{Kind: KindConfigKey, Name: key})
	}
	if src.Accounts != nil {
		collect(accounts(ctx, src.Accounts))
	}
	if src.DAGs != nil && src.WikiDir != "" {
		collect(wikiDirectories(ctx, src.DAGs, src.WikiDir))
	}

	slices.SortFunc(items, func(a, b Item) int {
		return cmp.Or(
			cmp.Compare(slices.Index(kindOrder, a.Kind), slices.Index(kindOrder, b.Kind)),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return slices.Compact(items), errors.Join(errs...)
}

// baseConfigs returns the workspace base configs with content. An empty file
// changes no workflow.
func baseConfigs(dagsDir string) ([]Item, error) {
	dir := workspace.BaseConfigDir(dagsDir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace base configs: %w", err)
	}
	var (
		items []Item
		errs  []error
	)
	for _, entry := range entries {
		path := workspace.BaseConfigPath(dagsDir, entry.Name())
		if !entry.IsDir() || path == "" {
			continue
		}
		data, err := os.ReadFile(path) //nolint:gosec // a base config under the configured DAGs directory
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("read workspace base config: %w", err))
			continue
		}
		if len(bytes.TrimSpace(data)) > 0 {
			items = append(items, Item{Kind: KindBaseConfig, Name: entry.Name(), Path: path})
		}
	}
	return items, errors.Join(errs...)
}

func secrets(ctx context.Context, lister SecretLister) ([]Item, error) {
	list, err := lister.List(ctx, secret.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	var items []Item
	for _, s := range list {
		if !secret.IsGlobalWorkspace(s.Workspace) {
			items = append(items, Item{Kind: KindSecret, Name: inWorkspace(s.Ref, s.Workspace)})
		}
	}
	return items, nil
}

func profileEntries(ctx context.Context, lister ProfileLister) ([]Item, error) {
	list, err := lister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	var items []Item
	for _, p := range list {
		name, ok := profile.InheritedWorkspaceName(p.Name)
		if !ok {
			continue
		}
		for _, entry := range p.Entries {
			items = append(items, Item{Kind: KindProfileEntry, Name: inWorkspace(entry.Key, name)})
		}
	}
	return items, nil
}

// notificationRoutes returns the workspace route sets that replace the
// global routes. One that inherits them changes nothing.
func notificationRoutes(ctx context.Context, lister RouteSetLister) ([]Item, error) {
	list, err := lister.ListRouteSets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notification routes: %w", err)
	}
	var items []Item
	for _, set := range list {
		if set.Scope == notification.RouteScopeWorkspace && !set.InheritGlobal {
			items = append(items, Item{Kind: KindNotificationRoute, Name: "workspace " + set.Workspace})
		}
	}
	return items, nil
}

// incidentPolicies returns the workspace policy sets that replace the global
// policies. One that inherits them changes nothing.
func incidentPolicies(ctx context.Context, lister PolicySetLister) ([]Item, error) {
	list, err := lister.ListPolicySets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list incident policies: %w", err)
	}
	var items []Item
	for _, set := range list {
		if set.Scope == incident.PolicyScopeWorkspace && !set.InheritParent {
			items = append(items, Item{Kind: KindIncidentPolicy, Name: "workspace " + set.Workspace})
		}
	}
	return items, nil
}

// accounts returns the users and API keys limited to some workspaces.
func accounts(ctx context.Context, lister AccountLister) ([]Item, error) {
	var (
		items []Item
		errs  []error
	)
	users, err := lister.ListUsers(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list users: %w", err))
	}
	for _, user := range users {
		if !auth.NormalizeWorkspaceAccess(user.WorkspaceAccess).All {
			items = append(items, Item{Kind: KindUser, Name: user.Username})
		}
	}
	keys, err := lister.ListAPIKeys(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list API keys: %w", err))
	}
	for _, key := range keys {
		if !auth.NormalizeWorkspaceAccess(key.WorkspaceAccess).All {
			items = append(items, Item{Kind: KindAPIKey, Name: key.Name})
		}
	}
	return items, errors.Join(errs...)
}

// wikiDirectories returns the wiki directories kept under a DAG's workspace.
func wikiDirectories(ctx context.Context, lister DAGLister, wikiDir string) ([]Item, error) {
	all := pagination.NewPaginator(1, math.MaxInt)
	result, _, err := lister.ListIncludingSearchPaths(ctx, persis.DAGListOptions{Paginator: &all})
	if err != nil {
		return nil, fmt.Errorf("list DAGs: %w", err)
	}
	var items []Item
	for _, dag := range result.Items {
		if dag.DAG == nil {
			continue
		}
		name, ok := workspace.WorkspaceNameFromLabels(dag.Labels)
		if !ok {
			continue
		}
		dir := filepath.Join(wikiDir, name, dag.Name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			items = append(items, Item{Kind: KindWikiDirectory, Name: name + "/" + dag.Name, Path: dir})
		}
	}
	return items, nil
}

func inWorkspace(name, workspaceName string) string {
	return fmt.Sprintf("%s (workspace %s)", name, workspaceName)
}
