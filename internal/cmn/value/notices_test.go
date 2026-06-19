// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportStepOutputReferenceNoticeUsesFallbackFieldLabel(t *testing.T) {
	t.Parallel()

	reasons := []value.ValueReferenceNoticeReason{
		value.ValueReferenceReasonUnknownStepID,
		value.ValueReferenceReasonUnknownOutputName,
		value.ValueReferenceReasonMissingDependency,
		value.ValueReferenceReasonSelfReference,
		value.ValueReferenceReasonNamespaceUnavailable,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()

			var collector value.ValueReferenceNoticeCollector
			value.ReportStepOutputReferenceNotice(&collector, "", "${steps.build.outputs.image}", reason)

			notices := collector.Notices()
			require.Len(t, notices, 1)
			assert.NotContains(t, notices[0].Message, "when  was evaluated")
			assert.NotContains(t, notices[0].Message, "because  has")
			assert.Contains(t, notices[0].Message, "the field")
		})
	}
}
