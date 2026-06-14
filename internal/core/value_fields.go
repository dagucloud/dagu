// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

type ResolvableOwnerKind string

const (
	ResolvableOwnerDAG  ResolvableOwnerKind = "dag"
	ResolvableOwnerStep ResolvableOwnerKind = "step"
)

type ResolvableField struct {
	Path           string
	Value          string
	OwnerKind      ResolvableOwnerKind
	OwnerStepIndex int
	OwnerStepID    string
	OwnerStepName  string
	Handler        HandlerType
	Mode           cmnvalue.Mode
	EnvIndex       int
	EnvName        string
	ContainerOwner string
	OutputLeafKind string
}

type resolvableFieldWalker struct {
	fields []ResolvableField
}

func ResolvableFields(dag *DAG) []ResolvableField {
	var w resolvableFieldWalker
	w.walkDAG(dag)
	return w.fields
}

func (w *resolvableFieldWalker) add(field ResolvableField) {
	if field.Value == "" {
		return
	}
	w.fields = append(w.fields, field)
}

func (w *resolvableFieldWalker) walkDAG(dag *DAG) {
	if dag == nil {
		return
	}
	root := ResolvableField{OwnerKind: ResolvableOwnerDAG, Mode: cmnvalue.ModeWorkflowValue, EnvIndex: -1}
	w.walkEnv("env", dag.Env, root)
	for i, dotenv := range dag.Dotenv {
		w.add(root.withPathValue(fmt.Sprintf("dotenv[%d]", i), dotenv))
	}
	w.add(root.withPathValue("shell", dag.Shell).withMode(cmnvalue.ModeShellCommand))
	for i, arg := range dag.ShellArgs {
		w.add(root.withPathValue(fmt.Sprintf("shell_args[%d]", i), arg).withMode(cmnvalue.ModeShellCommand))
	}
	w.add(root.withPathValue("working_dir", dag.WorkingDir))
	w.walkConditions("preconditions", dag.Preconditions, root)
	w.walkContainer("container", dag.Container, root.withContainerOwner("dag"))

	for i := range dag.Steps {
		w.walkStep(fmt.Sprintf("steps[%d]", i), i, dag.Steps[i], "")
	}
	w.walkHandlerStep("handler_on.init", dag.HandlerOn.Init, HandlerOnInit)
	w.walkHandlerStep("handler_on.success", dag.HandlerOn.Success, HandlerOnSuccess)
	w.walkHandlerStep("handler_on.failure", dag.HandlerOn.Failure, HandlerOnFailure)
	w.walkHandlerStep("handler_on.abort", dag.HandlerOn.Abort, HandlerOnAbort)
	w.walkHandlerStep("handler_on.exit", dag.HandlerOn.Exit, HandlerOnExit)
	w.walkHandlerStep("handler_on.wait", dag.HandlerOn.Wait, HandlerOnWait)
}

func (w *resolvableFieldWalker) walkHandlerStep(path string, step *Step, handler HandlerType) {
	if step == nil {
		return
	}
	w.walkStep(path, -1, *step, handler)
}

func (w *resolvableFieldWalker) walkStep(path string, index int, step Step, handler HandlerType) {
	base := ResolvableField{
		OwnerKind:      ResolvableOwnerStep,
		OwnerStepIndex: index,
		OwnerStepID:    step.ID,
		OwnerStepName:  step.Name,
		Handler:        handler,
		Mode:           cmnvalue.ModeWorkflowValue,
		EnvIndex:       -1,
	}

	w.add(base.withPathValue(path+".run", step.Script).withMode(cmnvalue.ModeShellCommand))
	w.add(base.withPathValue(path+".command", step.Command).withMode(cmnvalue.ModeDirectCommand))
	w.add(base.withPathValue(path+".cmd_with_args", step.CmdWithArgs).withMode(cmnvalue.ModeShellCommand))
	w.add(base.withPathValue(path+".cmd_args_sys", step.CmdArgsSys).withMode(cmnvalue.ModeDirectCommand))
	w.add(base.withPathValue(path+".shell_cmd_args", step.ShellCmdArgs).withMode(cmnvalue.ModeShellCommand))
	w.add(base.withPathValue(path+".shell", step.Shell).withMode(cmnvalue.ModeShellCommand))
	for i, arg := range step.Args {
		w.add(base.withPathValue(fmt.Sprintf("%s.args[%d]", path, i), arg).withMode(cmnvalue.ModeDirectCommand))
	}
	for i, arg := range step.ShellArgs {
		w.add(base.withPathValue(fmt.Sprintf("%s.shell_args[%d]", path, i), arg).withMode(cmnvalue.ModeShellCommand))
	}
	for i, cmd := range step.Commands {
		w.add(base.withPathValue(fmt.Sprintf("%s.run[%d].command", path, i), cmd.Command).withMode(cmnvalue.ModeDirectCommand))
		w.add(base.withPathValue(fmt.Sprintf("%s.run[%d].cmd_with_args", path, i), cmd.CmdWithArgs).withMode(cmnvalue.ModeShellCommand))
		for j, arg := range cmd.Args {
			w.add(base.withPathValue(fmt.Sprintf("%s.run[%d].args[%d]", path, i, j), arg).withMode(cmnvalue.ModeDirectCommand))
		}
	}

	w.walkStringLeaves(path+".with", step.ExecutorConfig.Config, base, "")
	w.add(base.withPathValue(path+".working_dir", step.Dir))
	w.walkEnv(path+".env", step.Env, base)
	w.walkConditions(path+".preconditions", step.Preconditions, base)
	if step.RepeatPolicy.Condition != nil {
		w.add(base.withPathValue(path+".repeat_policy.condition", step.RepeatPolicy.Condition.Condition))
	}
	if step.Parallel != nil {
		w.walkParallel(path+".parallel", step.Parallel, base)
	}
	w.add(base.withPathValue(path+".stdout", step.Stdout))
	w.add(base.withPathValue(path+".stdout.artifact", step.StdoutArtifact))
	w.add(base.withPathValue(path+".stderr", step.Stderr))
	w.add(base.withPathValue(path+".stderr.artifact", step.StderrArtifact))
	if step.StdoutOutputs != nil {
		w.walkStdoutOutputs(path+".stdout.outputs", step.StdoutOutputs, base)
	}
	w.walkStructuredOutput(path+".output", step.StructuredOutput, base)
	w.walkContainer(path+".container", step.Container, base.withContainerOwner("step"))
}

func (w *resolvableFieldWalker) walkConditions(path string, conditions []*Condition, base ResolvableField) {
	for i, condition := range conditions {
		if condition == nil {
			continue
		}
		w.add(base.withPathValue(fmt.Sprintf("%s[%d].condition", path, i), condition.Condition))
	}
}

func (w *resolvableFieldWalker) walkEnv(path string, env []string, base ResolvableField) {
	for i, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			value = entry
		}
		w.add(base.withPathValue(fmt.Sprintf("%s[%d]", path, i), value).withEnv(i, name))
	}
}

func (w *resolvableFieldWalker) walkParallel(path string, parallel *ParallelConfig, base ResolvableField) {
	w.add(base.withPathValue(path+".variable", parallel.Variable))
	for i, item := range parallel.Items {
		itemPath := fmt.Sprintf("%s.items[%d]", path, i)
		w.add(base.withPathValue(itemPath+".value", item.Value))
		keys := slices.Collect(maps.Keys(item.Params))
		slices.Sort(keys)
		for _, key := range keys {
			w.add(base.withPathValue(itemPath+".params."+key, item.Params[key]))
		}
	}
}

func (w *resolvableFieldWalker) walkStdoutOutputs(path string, outputs *StepOutputsConfig, base ResolvableField) {
	keys := slices.Collect(maps.Keys(outputs.Fields))
	slices.Sort(keys)
	for _, key := range keys {
		entry := outputs.Fields[key]
		field := base.withOutputLeaf("stdout.outputs")
		if entry.HasValue {
			w.walkStringLeaves(path+".fields."+key+".value", entry.Value, field, "value")
		}
		w.add(field.withPathValue(path+".fields."+key+".path", entry.Path).withOutputLeaf("path"))
	}
}

func (w *resolvableFieldWalker) walkStructuredOutput(path string, output map[string]StepOutputEntry, base ResolvableField) {
	keys := slices.Collect(maps.Keys(output))
	slices.Sort(keys)
	for _, key := range keys {
		entry := output[key]
		field := base.withOutputLeaf("output")
		if entry.HasValue {
			w.walkStringLeaves(path+"."+key+".value", entry.Value, field, "value")
		}
		w.add(field.withPathValue(path+"."+key+".path", entry.Path).withOutputLeaf("path"))
	}
}

func (w *resolvableFieldWalker) walkContainer(path string, container *Container, base ResolvableField) {
	if container == nil {
		return
	}
	w.add(base.withPathValue(path+".exec", container.Exec))
	w.add(base.withPathValue(path+".image", container.Image))
	w.add(base.withPathValue(path+".name", container.Name))
	w.add(base.withPathValue(path+".user", container.User))
	w.add(base.withPathValue(path+".working_dir", container.WorkingDir))
	w.add(base.withPathValue(path+".network", container.Network))
	for i, value := range container.Volumes {
		w.add(base.withPathValue(fmt.Sprintf("%s.volumes[%d]", path, i), value))
	}
	for i, value := range container.Ports {
		w.add(base.withPathValue(fmt.Sprintf("%s.ports[%d]", path, i), value))
	}
	w.walkEnv(path+".env", container.Env, base)
	for i, value := range container.Command {
		w.add(base.withPathValue(fmt.Sprintf("%s.command[%d]", path, i), value).withMode(cmnvalue.ModeDirectCommand))
	}
	for i, value := range container.Shell {
		w.add(base.withPathValue(fmt.Sprintf("%s.shell[%d]", path, i), value).withMode(cmnvalue.ModeShellCommand))
	}
}

func (w *resolvableFieldWalker) walkStringLeaves(path string, raw any, base ResolvableField, outputLeaf string) {
	switch value := raw.(type) {
	case nil:
		return
	case string:
		w.add(base.withPathValue(path, value).withOutputLeaf(outputLeaf))
	case []string:
		for i, item := range value {
			w.add(base.withPathValue(fmt.Sprintf("%s[%d]", path, i), item).withOutputLeaf(outputLeaf))
		}
	case []any:
		for i, item := range value {
			w.walkStringLeaves(fmt.Sprintf("%s[%d]", path, i), item, base, outputLeaf)
		}
	case map[string]any:
		keys := slices.Collect(maps.Keys(value))
		slices.Sort(keys)
		for _, key := range keys {
			w.walkStringLeaves(path+"."+key, value[key], base, outputLeaf)
		}
	case map[string]string:
		keys := slices.Collect(maps.Keys(value))
		slices.Sort(keys)
		for _, key := range keys {
			w.add(base.withPathValue(path+"."+key, value[key]).withOutputLeaf(outputLeaf))
		}
	default:
		w.walkReflectStringLeaves(path, raw, base, outputLeaf)
	}
}

func (w *resolvableFieldWalker) walkReflectStringLeaves(path string, raw any, base ResolvableField, outputLeaf string) {
	rv := reflect.ValueOf(raw)
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			w.walkStringLeaves(fmt.Sprintf("%s[%d]", path, i), rv.Index(i).Interface(), base, outputLeaf)
		}
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return
		}
		keys := make([]string, 0, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			keys = append(keys, iter.Key().String())
		}
		slices.Sort(keys)
		for _, key := range keys {
			w.walkStringLeaves(path+"."+key, rv.MapIndex(reflect.ValueOf(key)).Interface(), base, outputLeaf)
		}
	}
}

func (f ResolvableField) withPathValue(path, value string) ResolvableField {
	f.Path = path
	f.Value = value
	return f
}

func (f ResolvableField) withMode(mode cmnvalue.Mode) ResolvableField {
	f.Mode = mode
	return f
}

func (f ResolvableField) withEnv(index int, name string) ResolvableField {
	f.EnvIndex = index
	f.EnvName = name
	return f
}

func (f ResolvableField) withContainerOwner(owner string) ResolvableField {
	f.ContainerOwner = owner
	return f
}

func (f ResolvableField) withOutputLeaf(kind string) ResolvableField {
	if kind != "" {
		f.OutputLeafKind = kind
	}
	return f
}
