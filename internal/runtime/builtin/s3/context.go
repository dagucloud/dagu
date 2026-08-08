// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// s3ConfigCtxKey is the context key for DAG-level S3 configuration.
type s3ConfigCtxKey struct{}

// WithS3Config creates a new context with DAG-level S3 configuration.
// This allows the S3 executor to inherit default settings from the DAG.
func WithS3Config(ctx context.Context, cfg *ir.S3Config) context.Context {
	return context.WithValue(ctx, s3ConfigCtxKey{}, cfg)
}

// getS3ConfigFromContext retrieves DAG-level S3Config from the context.
// Returns nil if no S3 configuration is set in the context.
func getS3ConfigFromContext(ctx context.Context) *ir.S3Config {
	if cfg, ok := ctx.Value(s3ConfigCtxKey{}).(*ir.S3Config); ok {
		return cfg
	}
	return nil
}
