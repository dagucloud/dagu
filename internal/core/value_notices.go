// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
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
	rootEnvScope := reportEnvValueReferenceNotices(
		dag.Env,
		"env",
		cmnvalue.DAGEnvField,
		cmnvalue.EnvSourceDAGEnv,
		staticScope,
		runtimeScope,
		cmnvalue.NewEnvScope(nil, false),
		sink,
	)
	runtimeScope.Env = rootEnvScope
	if dag.Container != nil {
		reportEnvValueReferenceNotices(
			dag.Container.Env,
			"container.env",
			cmnvalue.ContainerEnvField,
			cmnvalue.EnvSourceDAGEnv,
			staticScope,
			runtimeScope,
			rootEnvScope,
			sink,
		)
	}
	reportStepEnvValueReferenceNotices(dag, staticScope, runtimeScope, rootEnvScope, sink)

	for _, field := range ReferenceFields(dag) {
		if isEnvReferenceFieldPath(field.Path) {
			continue
		}
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

func reportStepEnvValueReferenceNotices(
	dag *DAG,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	for i := range dag.Steps {
		reportSingleStepEnvValueReferenceNotices(
			fmt.Sprintf("steps[%d]", i),
			dag.Steps[i],
			staticScope,
			runtimeScope,
			rootEnvScope,
			sink,
		)
	}
	reportHandlerStepEnvValueReferenceNotices("handler_on.init", dag.HandlerOn.Init, staticScope, runtimeScope, rootEnvScope, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.success", dag.HandlerOn.Success, staticScope, runtimeScope, rootEnvScope, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.failure", dag.HandlerOn.Failure, staticScope, runtimeScope, rootEnvScope, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.abort", dag.HandlerOn.Abort, staticScope, runtimeScope, rootEnvScope, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.exit", dag.HandlerOn.Exit, staticScope, runtimeScope, rootEnvScope, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.wait", dag.HandlerOn.Wait, staticScope, runtimeScope, rootEnvScope, sink)
}

func reportHandlerStepEnvValueReferenceNotices(
	path string,
	step *Step,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	if step == nil {
		return
	}
	reportSingleStepEnvValueReferenceNotices(path, *step, staticScope, runtimeScope, rootEnvScope, sink)
}

func reportSingleStepEnvValueReferenceNotices(
	path string,
	step Step,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	stepEnvScope := reportEnvValueReferenceNotices(
		step.Env,
		path+".env",
		cmnvalue.StepEnvField,
		cmnvalue.EnvSourceStepEnv,
		staticScope,
		runtimeScope,
		rootEnvScope,
		sink,
	)
	if step.Container != nil {
		runtimeScope.Env = stepEnvScope
		reportEnvValueReferenceNotices(
			step.Container.Env,
			path+".container.env",
			cmnvalue.ContainerEnvField,
			cmnvalue.EnvSourceStepEnv,
			staticScope,
			runtimeScope,
			stepEnvScope,
			sink,
		)
	}
}

func reportEnvValueReferenceNotices(
	env []string,
	path string,
	fieldForPath func(string) cmnvalue.Field,
	source cmnvalue.EnvSource,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	scope *cmnvalue.EnvScope,
	sink cmnvalue.ValueReferenceNoticeSink,
) *cmnvalue.EnvScope {
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, false)
	}
	for i, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		fieldPath := fmt.Sprintf("%s[%d]", path, i)
		fieldSink := valueReferenceNoticeFieldSink{sink: sink, fieldPath: fieldPath}
		runtimeScope.Env = scope
		resolver := cmnvalue.NewResolver(
			staticScope,
			runtimeScope,
			cmnvalue.WithValueReferenceNotices(fieldSink),
		)
		resolved, err := resolver.String(context.Background(), value, fieldForPath(fieldPath))
		if err != nil {
			resolved = value
		}
		cmnvalue.ReportUnresolvedEnvExpansionNotices(value, fieldPath, scope, fieldSink)
		if cmnvalue.ValidEnvName(key) {
			scope = scope.WithEntry(key, resolved, source)
		}
	}
	return scope
}

func isEnvReferenceFieldPath(path string) bool {
	return strings.HasPrefix(path, "env[") ||
		strings.HasPrefix(path, "container.env[") ||
		strings.Contains(path, ".env[") ||
		strings.Contains(path, ".container.env[")
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
