// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/dagucloud/dagu/internal/cmn/config"
)

func WebUIEmbeddedForTest() bool {
	return webUIEmbedded
}

func NewRouteTestServerForTest(cfg *config.Config) *Server {
	return &Server{
		config: cfg,
		funcsConfig: funcsConfig{
			BasePath: cfg.Server.BasePath,
		},
	}
}

func SetupRoutesForTest(ctx context.Context, srv *Server, r *chi.Mux) error {
	return srv.setupRoutes(ctx, r)
}
