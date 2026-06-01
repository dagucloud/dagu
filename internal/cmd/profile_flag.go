// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/profile"
)

func runtimeProfileNameParam(ctx *Context) (string, error) {
	name, err := ctx.StringParam("profile")
	if err != nil {
		return "", fmt.Errorf("failed to get profile: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	if err := profile.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}
