// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
)

// newEventService opens the run events of the project whose event directory
// is dir, or returns nil when the event store is off or cannot open.
func newEventService(ctx context.Context, cfg *config.Config, dir string) *eventstore.Service {
	if !cfg.EventStore.Enabled {
		return nil
	}
	store, err := fileeventstore.New(dir)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(err))
		return nil
	}
	return eventstore.New(store)
}
