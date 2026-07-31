// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings

import (
	"context"
	"errors"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/profile"
)

// ErrProfileStoreUnavailable means a selected runtime profile cannot be checked.
var ErrProfileStoreUnavailable = errors.New("runtime profile store is not configured")

type ProfileReferenceError struct {
	Name string
	Err  error
}

func (e *ProfileReferenceError) Error() string {
	return e.Err.Error()
}

func (e *ProfileReferenceError) Unwrap() error {
	return e.Err
}

func ResolveProfile(
	ctx context.Context,
	settingsStore Store,
	profileStore profile.Store,
	dagName string,
	workspaceName string,
) (string, error) {
	profileName, err := dagDefaultProfile(ctx, settingsStore, dagName)
	if err != nil {
		return "", err
	}
	if profileName == "" {
		profileName, err = workspaceDefaultProfile(ctx, profileStore, workspaceName)
		if err != nil {
			return "", err
		}
	}
	return ensureRunnableProfile(ctx, profileStore, profileName)
}

func dagDefaultProfile(ctx context.Context, settingsStore Store, dagName string) (string, error) {
	if settingsStore == nil {
		return "", nil
	}
	settings, err := settingsStore.Get(ctx, dagName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(settings.Profile), nil
}

func workspaceDefaultProfile(ctx context.Context, profileStore profile.Store, workspaceName string) (string, error) {
	workspaceName = strings.TrimSpace(workspaceName)
	if workspaceName == "" {
		return "", nil
	}
	if profileStore == nil {
		return "", nil
	}
	ref, err := profile.WorkspaceInheritedRef(workspaceName)
	if err != nil {
		return "", err
	}
	defaults, err := profileStore.GetInherited(ctx, ref)
	if err != nil {
		if errors.Is(err, profile.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(defaults.DefaultProfile), nil
}

func ensureRunnableProfile(ctx context.Context, profileStore profile.Store, profileName string) (string, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return "", nil
	}
	if profileStore == nil {
		return "", ErrProfileStoreUnavailable
	}
	resolved, err := profile.NewManager(profileStore, nil).EnsureRunnable(ctx, profileName)
	if err != nil {
		return "", &ProfileReferenceError{Name: profileName, Err: err}
	}
	return resolved.Name, nil
}
