// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

// ReportValueReferenceNotices reports passive notices for value references in dag.
func ReportValueReferenceNotices(dag *DAG, sink cmnvalue.ValueReferenceNoticeSink) {
	if dag == nil || sink == nil {
		return
	}

	staticScope := cmnvalue.StaticScope{Consts: cmnvalue.Values(dag.Consts), Params: dag.ParamDeclarations()}
	runtimeScope := cmnvalue.RuntimeScope{
		Consts: cmnvalue.Values(dag.Consts),
		Params: dag.ParamValues(),
		Steps:  map[string]cmnvalue.StepInfo{},
	}
	for _, field := range ReferenceFields(dag) {
		if !strings.Contains(field.Value, "$") {
			continue
		}
		resolver := cmnvalue.NewResolver(
			staticScope,
			runtimeScope,
			cmnvalue.WithValueReferenceNotices(valueReferenceNoticeFieldSink{
				sink:      sink,
				fieldPath: field.noticeFieldPath(),
			}),
		)
		_, _ = resolver.String(context.Background(), field.Value, field.Field)
	}
}

type valueReferenceNoticeFieldSink struct {
	sink      cmnvalue.ValueReferenceNoticeSink
	fieldPath string
}

func (s valueReferenceNoticeFieldSink) Report(notice cmnvalue.ValueReferenceNotice) {
	if s.fieldPath != "" {
		notice.FieldPath = s.fieldPath
	}
	s.sink.Report(notice)
}
