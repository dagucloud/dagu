// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"regexp"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/diagnostic"
)

var generatedRunFieldPathPattern = regexp.MustCompile(`^((?:steps\[\d+\])|(?:handler_on\.[^.]+))\.run\[(\d+)\](?:\.(?:command|cmd_with_args|args\[\d+\]))?$`)

// ReportValueReferenceDiagnostics reports passive diagnostics for value references in dag.
func ReportValueReferenceDiagnostics(dag *DAG, sink diagnostic.Sink) {
	if dag == nil || sink == nil {
		return
	}

	normalizedSink := normalizedValueDiagnosticSink{sink: sink}
	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Consts: cmnvalue.Values(dag.Consts), Params: dag.ParamDeclarations()},
		cmnvalue.RuntimeScope{
			Consts: cmnvalue.Values(dag.Consts),
			Params: dag.ParamValues(),
			Steps:  map[string]cmnvalue.StepInfo{},
		},
		cmnvalue.WithDiagnostics(normalizedSink),
	)
	for _, field := range ReferenceFields(dag) {
		if !strings.Contains(field.Value, "$") {
			continue
		}
		_, _ = resolver.String(context.Background(), field.Value, field.Field)
	}
}

type normalizedValueDiagnosticSink struct {
	sink diagnostic.Sink
}

func (s normalizedValueDiagnosticSink) Report(d diagnostic.Diagnostic) {
	d.Location.FieldPath = normalizeValueDiagnosticFieldPath(d.Location.FieldPath)
	s.sink.Report(d)
}

func normalizeValueDiagnosticFieldPath(path string) string {
	matches := generatedRunFieldPathPattern.FindStringSubmatch(path)
	if len(matches) == 3 {
		return matches[1] + ".run[" + matches[2] + "]"
	}
	return path
}
