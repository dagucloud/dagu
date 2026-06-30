// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"

	"github.com/dagucloud/dagu/internal/auth"
)

func adminCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{
		Username: "admin",
		Role:     auth.RoleAdmin,
	})
}
