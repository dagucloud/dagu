// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

var (
	ExtractWebhookToken         = extractWebhookToken
	MarshalWebhookPayload       = marshalWebhookPayload
	MarshalWebhookHeaders       = marshalWebhookHeaders
	IsWebhookTriggerPath        = isWebhookTriggerPath
	WithRawBody                 = withRawBody
	WithRequestHeaders          = withRequestHeaders
	BuildArtifactPreviewForTest = buildArtifactPreview
)

const ArtifactTextPreviewMaxBytesForTest = artifactTextPreviewMaxBytes

func NextRunProjectionForTest(ctx context.Context, a *API) func(*core.DAG, time.Time) time.Time {
	return a.nextRunProjection(ctx)
}
