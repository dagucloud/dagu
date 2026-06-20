// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

// Export internal functions for testing
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
