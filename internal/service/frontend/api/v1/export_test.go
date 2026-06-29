// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

// Test exports expose internal helpers to external-package tests.
var (
	ExtractWebhookToken         = extractWebhookToken
	MarshalWebhookPayload       = marshalWebhookPayload
	MarshalWebhookHeaders       = marshalWebhookHeaders
	IsWebhookTriggerPath        = isWebhookTriggerPath
	WithRawBody                 = withRawBody
	WithRequestHeaders          = withRequestHeaders
	BuildArtifactPreviewForTest = buildArtifactPreview
	ToDAGRunSummaryForTest      = toDAGRunSummary
)

// ArtifactTextPreviewMaxBytesForTest exposes the artifact preview size limit to external-package tests.
const ArtifactTextPreviewMaxBytesForTest = artifactTextPreviewMaxBytes

// NextRunProjectionForTest returns the API next-run projector for external-package tests.
func NextRunProjectionForTest(ctx context.Context, a *API) func(*core.DAG, time.Time) time.Time {
	return a.nextRunProjection(ctx)
}
