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

func TestValueReferenceNoticeReasonClass(t *testing.T) {
	t.Parallel()

	defects := []value.ValueReferenceNoticeReason{
		value.ValueReferenceReasonUnknownStepID,
		value.ValueReferenceReasonUnknownOutputName,
		value.ValueReferenceReasonMissingDependency,
		value.ValueReferenceReasonSelfReference,
		value.ValueReferenceReasonUnknownContextField,
		value.ValueReferenceReasonUnknownConstName,
	}
	for _, reason := range defects {
		assert.Equal(t, value.NoticeClassDefect, reason.Class(), string(reason))
	}

	runtimeOnly := []value.ValueReferenceNoticeReason{
		value.ValueReferenceReasonNamespaceUnavailable,
		value.ValueReferenceReasonUnknownEnvBinding,
	}
	for _, reason := range runtimeOnly {
		assert.Equal(t, value.NoticeClassRuntimeOnly, reason.Class(), string(reason))
	}
}

func TestValueReferenceNoticeCarriesClass(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	value.ReportStepOutputReferenceNotice(
		&collector,
		"steps[1].run",
		"${steps.build.outputs.tag}",
		value.ValueReferenceReasonUnknownOutputName,
	)

	notices := collector.Notices()
	require.Len(t, notices, 1)
	assert.Equal(t, value.NoticeClassDefect, notices[0].Class)
}

func TestStepOutputNamespaceUnavailableIsDefect(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	value.ReportStepOutputReferenceNotice(
		&collector,
		"env[0]",
		"${steps.build.outputs.tag}",
		value.ValueReferenceReasonNamespaceUnavailable,
	)

	notices := collector.Notices()
	require.Len(t, notices, 1)
	assert.Equal(t, value.NoticeClassDefect, notices[0].Class)
}
