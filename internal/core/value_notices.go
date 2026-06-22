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
		Consts:         cmnvalue.Values(dag.Consts),
		Params:         dag.ParamValues(),
		Steps:          map[string]cmnvalue.StepInfo{},
		BuiltinContext: noticeBuiltinContext(dag.Name, "", ""),
	}
	stepOutputNotices := newStepOutputNoticeContext(dag)
	rootEnvScope := reportEnvValueReferenceNotices(
		dag.Env,
		"env",
		dag.Name,
		"",
		"",
		cmnvalue.DAGEnvField,
		cmnvalue.EnvSourceDAGEnv,
		staticScope,
		runtimeScope,
		cmnvalue.NewEnvScope(nil, false),
		stepOutputNotices,
		sink,
	)
	runtimeScope.Env = rootEnvScope
	if dag.Container != nil {
		reportEnvValueReferenceNotices(
			dag.Container.Env,
			"container.env",
			dag.Name,
			"",
			"",
			cmnvalue.ContainerEnvField,
			cmnvalue.EnvSourceDAGEnv,
			staticScope,
			runtimeScope,
			rootEnvScope,
			stepOutputNotices,
			sink,
		)
	}
	reportStepEnvValueReferenceNotices(dag, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)

	for _, field := range ReferenceFields(dag) {
		if isEnvReferenceFieldPath(field.Path) {
			continue
		}
		if !strings.Contains(field.Value, "$") {
			continue
		}
		foreachItemScopeField := isForeachItemScopeFieldPath(field.noticeFieldPath())
		if !foreachItemScopeField {
			stepOutputNotices.report(field.noticeFieldPath(), field.Value, field.OwnerStepName, field.OwnerStepID, sink)
		}
		fieldRuntimeScope := runtimeScope
		fieldRuntimeScope.BuiltinContext = noticeBuiltinContext(dag.Name, field.OwnerStepName, field.OwnerStepID)
		resolver := cmnvalue.NewResolver(
			staticScope,
			fieldRuntimeScope,
			cmnvalue.WithValueReferenceNotices(valueReferenceNoticeFieldSink{
				sink:                         sink,
				fieldPath:                    field.noticeFieldPath(),
				suppressStepOutputReferences: true,
				suppressForeachReferences:    foreachItemScopeField,
			}),
		)
		_, _ = resolver.String(context.Background(), field.Value, field.Field)
	}
}

func isForeachItemScopeFieldPath(path string) bool {
	return strings.Contains(path, ".foreach.key") ||
		strings.Contains(path, ".foreach.steps[") ||
		strings.Contains(path, ".foreach.collect.")
}

func reportStepEnvValueReferenceNotices(
	dag *DAG,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	stepOutputNotices *stepOutputNoticeContext,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	for i := range dag.Steps {
		reportSingleStepEnvValueReferenceNotices(
			fmt.Sprintf("steps[%d]", i),
			dag.Steps[i],
			dag.Name,
			staticScope,
			runtimeScope,
			rootEnvScope,
			stepOutputNotices,
			sink,
		)
	}
	reportHandlerStepEnvValueReferenceNotices("handler_on.init", dag.HandlerOn.Init, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.success", dag.HandlerOn.Success, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.failure", dag.HandlerOn.Failure, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.abort", dag.HandlerOn.Abort, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.exit", dag.HandlerOn.Exit, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
	reportHandlerStepEnvValueReferenceNotices("handler_on.wait", dag.HandlerOn.Wait, dag.Name, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
}

func reportHandlerStepEnvValueReferenceNotices(
	path string,
	step *Step,
	dagName string,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	stepOutputNotices *stepOutputNoticeContext,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	if step == nil {
		return
	}
	reportSingleStepEnvValueReferenceNotices(path, *step, dagName, staticScope, runtimeScope, rootEnvScope, stepOutputNotices, sink)
}

func reportSingleStepEnvValueReferenceNotices(
	path string,
	step Step,
	dagName string,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	rootEnvScope *cmnvalue.EnvScope,
	stepOutputNotices *stepOutputNoticeContext,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	stepEnvScope := reportEnvValueReferenceNotices(
		step.Env,
		path+".env",
		dagName,
		step.Name,
		step.ID,
		cmnvalue.StepEnvField,
		cmnvalue.EnvSourceStepEnv,
		staticScope,
		runtimeScope,
		rootEnvScope,
		stepOutputNotices,
		sink,
	)
	if step.Container != nil {
		runtimeScope.Env = stepEnvScope
		reportEnvValueReferenceNotices(
			step.Container.Env,
			path+".container.env",
			dagName,
			step.Name,
			step.ID,
			cmnvalue.ContainerEnvField,
			cmnvalue.EnvSourceStepEnv,
			staticScope,
			runtimeScope,
			stepEnvScope,
			stepOutputNotices,
			sink,
		)
	}
}

func reportEnvValueReferenceNotices(
	env []string,
	path string,
	dagName string,
	ownerStepName string,
	ownerStepID string,
	fieldForPath func(string) cmnvalue.Field,
	source cmnvalue.EnvSource,
	staticScope cmnvalue.StaticScope,
	runtimeScope cmnvalue.RuntimeScope,
	scope *cmnvalue.EnvScope,
	stepOutputNotices *stepOutputNoticeContext,
	sink cmnvalue.ValueReferenceNoticeSink,
) *cmnvalue.EnvScope {
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, false)
	}
	for i, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		fieldPath := fmt.Sprintf("%s[%d]", path, i)
		stepOutputNotices.report(fieldPath, value, ownerStepName, ownerStepID, sink)
		fieldSink := valueReferenceNoticeFieldSink{sink: sink, fieldPath: fieldPath, suppressStepOutputReferences: true}
		runtimeScope.Env = scope
		runtimeScope.BuiltinContext = noticeBuiltinContext(dagName, ownerStepName, ownerStepID)
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

func noticeBuiltinContext(dagName, stepName, stepID string) cmnvalue.BuiltinContext {
	values := make(map[string]string)
	if dagName != "" {
		values["context.dag.name"] = dagName
	}
	if stepName != "" {
		values["context.step.name"] = stepName
	}
	if stepID != "" {
		values["context.step.id"] = stepID
	}
	return cmnvalue.NewBuiltinContext(values)
}

func isEnvReferenceFieldPath(path string) bool {
	return strings.HasPrefix(path, "env[") ||
		strings.HasPrefix(path, "container.env[") ||
		strings.Contains(path, ".env[") ||
		strings.Contains(path, ".container.env[")
}

type stepOutputNoticeContext struct {
	stepsByID      map[string]Step
	outputNames    map[string]map[string]struct{}
	depsByStepName map[string][]string
}

func newStepOutputNoticeContext(dag *DAG) *stepOutputNoticeContext {
	ctx := &stepOutputNoticeContext{
		stepsByID:      make(map[string]Step),
		outputNames:    make(map[string]map[string]struct{}),
		depsByStepName: make(map[string][]string),
	}
	if dag == nil {
		return ctx
	}
	for _, step := range dag.Steps {
		ctx.depsByStepName[step.Name] = append([]string(nil), step.Depends...)
		if step.ID == "" {
			continue
		}
		ctx.stepsByID[step.ID] = step
		names := make(map[string]struct{}, len(step.Outputs))
		for _, output := range step.Outputs {
			names[output.Name] = struct{}{}
		}
		ctx.outputNames[step.ID] = names
	}
	return ctx
}

func (c *stepOutputNoticeContext) report(
	fieldPath string,
	value string,
	ownerStepName string,
	ownerStepID string,
	sink cmnvalue.ValueReferenceNoticeSink,
) {
	if c == nil || sink == nil {
		return
	}
	for _, ref := range cmnvalue.StepOutputReferences(value) {
		reason, ok := c.reason(fieldPath, ownerStepName, ownerStepID, ref)
		if !ok {
			continue
		}
		cmnvalue.ReportStepOutputReferenceNotice(sink, fieldPath, ref.Expression, reason)
	}
}

func (c *stepOutputNoticeContext) reason(
	fieldPath string,
	ownerStepName string,
	ownerStepID string,
	ref cmnvalue.StepOutputReference,
) (cmnvalue.ValueReferenceNoticeReason, bool) {
	if ownerStepName == "" || strings.HasPrefix(fieldPath, "handler_on.") {
		return cmnvalue.ValueReferenceReasonNamespaceUnavailable, true
	}
	producer, ok := c.stepsByID[ref.StepName]
	if !ok {
		return cmnvalue.ValueReferenceReasonUnknownStepID, true
	}
	if ownerStepID != "" && ownerStepID == ref.StepName {
		return cmnvalue.ValueReferenceReasonSelfReference, true
	}
	outputName := ""
	if len(ref.Path) > 0 {
		outputName = ref.Path[0]
	}
	if _, ok := c.outputNames[producer.ID][outputName]; !ok {
		return cmnvalue.ValueReferenceReasonUnknownOutputName, true
	}
	if !c.dependsOn(ownerStepName, producer.Name) {
		return cmnvalue.ValueReferenceReasonMissingDependency, true
	}
	return "", false
}

func (c *stepOutputNoticeContext) dependsOn(ownerStepName, producerStepName string) bool {
	if ownerStepName == "" || producerStepName == "" {
		return false
	}
	seen := make(map[string]struct{})
	queue := append([]string(nil), c.depsByStepName[ownerStepName]...)
	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]
		if dep == producerStepName {
			return true
		}
		if _, ok := seen[dep]; ok {
			continue
		}
		seen[dep] = struct{}{}
		queue = append(queue, c.depsByStepName[dep]...)
	}
	return false
}

type valueReferenceNoticeFieldSink struct {
	sink                         cmnvalue.ValueReferenceNoticeSink
	fieldPath                    string
	suppressStepOutputReferences bool
	suppressForeachReferences    bool
}

func (s valueReferenceNoticeFieldSink) Report(notice cmnvalue.ValueReferenceNotice) {
	if s.suppressStepOutputReferences && cmnvalue.IsStepOutputReferenceToken(notice.Token) {
		return
	}
	if s.suppressForeachReferences && strings.HasPrefix(notice.Token, "${foreach.") {
		return
	}
	if s.fieldPath != "" {
		notice.FieldPath = s.fieldPath
	}
	s.sink.Report(notice)
}
