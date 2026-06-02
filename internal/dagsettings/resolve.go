// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/profile"
)

func ResolveProfile(ctx context.Context, settingsStore Store, profileStore profile.Store, dagName string) (string, error) {
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
	profileName := strings.TrimSpace(settings.Profile)
	if profileName == "" {
		return "", nil
	}
	if profileStore == nil {
		return "", fmt.Errorf("runtime profile store is not configured")
	}
	resolved, err := profile.NewManager(profileStore, nil).EnsureRunnable(ctx, profileName)
	if err != nil {
		return "", err
	}
	return resolved.Name, nil
}
