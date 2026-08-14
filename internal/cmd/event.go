// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

func newEventService(ctx context.Context, cfg *config.Config) *eventstore.Service {
	if !cfg.EventStore.Enabled {
		return nil
	}
	store, err := persisfile.NewEventStore(cfg)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		return nil
	}
	if store == nil {
		return nil
	}
	return eventstore.New(store)
}
