// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"

func reportNamespaceUnavailableStepOutputReferences(sink cmnvalue.ValueReferenceNoticeSink, field, raw string) {
	for _, ref := range cmnvalue.StepOutputReferences(raw) {
		cmnvalue.ReportStepOutputReferenceNotice(
			sink,
			field,
			ref.Expression,
			cmnvalue.ValueReferenceReasonNamespaceUnavailable,
		)
	}
}

func buildNoticeSink(sink cmnvalue.ValueReferenceNoticeSink) cmnvalue.ValueReferenceNoticeSink {
	return cmnvalue.SuppressStepOutputReferenceNotices(sink)
}
