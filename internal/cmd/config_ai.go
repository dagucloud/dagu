// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// logAIConfig logs the value that applies for each key of the block ai, with its
// source, at the start of a process that reads the block (Spec 093). The block
// has no secret.
func logAIConfig(ctx *Context) {
	ai := ctx.Config.AI
	values := ai.Values()
	attrs := make([]slog.Attr, 0, len(values))
	for _, doc := range config.AIKeyDocs() {
		attrs = append(attrs, slog.String("ai."+doc.Key, values[doc.Key]+" ("+ai.Sources[doc.Key]+")"))
	}
	logger.Info(ctx, "AI configuration", attrs...)
}
