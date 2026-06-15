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

type ReferenceField struct {
	Path          string
	Value         string
	OwnerStepName string
	Mode          cmnvalue.Mode
}

type referenceFieldWalker struct {
	fields []ReferenceField
}

func ReferenceFields(dag *DAG) []ReferenceField {
	var w referenceFieldWalker
	w.walkDAG(dag)
	return w.fields
}

func (w *referenceFieldWalker) add(field ReferenceField) {
	if field.Value == "" {
		return
	}
	w.fields = append(w.fields, field)
}

func (w *referenceFieldWalker) walkDAG(dag *DAG) {
	if dag == nil {
		return
	}
	root := ReferenceField{Mode: cmnvalue.ModeWorkflowValue}
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
	w.walkContainer("container", dag.Container, root)

	for i := range dag.Steps {
		w.walkStep(fmt.Sprintf("steps[%d]", i), dag.Steps[i])
	}
	w.walkHandlerStep("handler_on.init", dag.HandlerOn.Init)
	w.walkHandlerStep("handler_on.success", dag.HandlerOn.Success)
	w.walkHandlerStep("handler_on.failure", dag.HandlerOn.Failure)
	w.walkHandlerStep("handler_on.abort", dag.HandlerOn.Abort)
	w.walkHandlerStep("handler_on.exit", dag.HandlerOn.Exit)
	w.walkHandlerStep("handler_on.wait", dag.HandlerOn.Wait)
}

func (w *referenceFieldWalker) walkHandlerStep(path string, step *Step) {
	if step == nil {
		return
	}
	w.walkStep(path, *step)
}

func (w *referenceFieldWalker) walkStep(path string, step Step) {
	base := ReferenceField{
		OwnerStepName: step.Name,
		Mode:          cmnvalue.ModeWorkflowValue,
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

	w.walkStringLeaves(path+".with", step.ExecutorConfig.Config, base)
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
	w.walkContainer(path+".container", step.Container, base)
}

func (w *referenceFieldWalker) walkConditions(path string, conditions []*Condition, base ReferenceField) {
	for i, condition := range conditions {
		if condition == nil {
			continue
		}
		w.add(base.withPathValue(fmt.Sprintf("%s[%d].condition", path, i), condition.Condition))
	}
}

func (w *referenceFieldWalker) walkEnv(path string, env []string, base ReferenceField) {
	for i, entry := range env {
		_, value, ok := strings.Cut(entry, "=")
		if !ok {
			value = entry
		}
		w.add(base.withPathValue(fmt.Sprintf("%s[%d]", path, i), value))
	}
}

func (w *referenceFieldWalker) walkParallel(path string, parallel *ParallelConfig, base ReferenceField) {
	w.add(base.withPathValue(path+".variable", parallel.Variable))
	for i, item := range parallel.Items {
		itemPath := fmt.Sprintf("%s.items[%d]", path, i)
		w.add(base.withPathValue(itemPath+".value", item.Value))
		for _, key := range sortedStringKeys(item.Params) {
			w.add(base.withPathValue(itemPath+".params."+key, item.Params[key]))
		}
	}
}

func (w *referenceFieldWalker) walkStdoutOutputs(path string, outputs *StepOutputsConfig, base ReferenceField) {
	for _, key := range sortedStringKeys(outputs.Fields) {
		entry := outputs.Fields[key]
		if entry.HasValue {
			w.walkStringLeaves(path+".fields."+key+".value", entry.Value, base)
		}
		w.add(base.withPathValue(path+".fields."+key+".path", entry.Path))
	}
}

func (w *referenceFieldWalker) walkStructuredOutput(path string, output map[string]StepOutputEntry, base ReferenceField) {
	for _, key := range sortedStringKeys(output) {
		entry := output[key]
		if entry.HasValue {
			w.walkStringLeaves(path+"."+key+".value", entry.Value, base)
		}
		w.add(base.withPathValue(path+"."+key+".path", entry.Path))
	}
}

func (w *referenceFieldWalker) walkContainer(path string, container *Container, base ReferenceField) {
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

func (w *referenceFieldWalker) walkStringLeaves(path string, raw any, base ReferenceField) {
	switch value := raw.(type) {
	case nil:
		return
	case string:
		w.add(base.withPathValue(path, value))
	case []string:
		for i, item := range value {
			w.add(base.withPathValue(fmt.Sprintf("%s[%d]", path, i), item))
		}
	case []any:
		for i, item := range value {
			w.walkStringLeaves(fmt.Sprintf("%s[%d]", path, i), item, base)
		}
	case map[string]any:
		for _, key := range sortedStringKeys(value) {
			w.walkStringLeaves(path+"."+key, value[key], base)
		}
	case map[string]string:
		for _, key := range sortedStringKeys(value) {
			w.add(base.withPathValue(path+"."+key, value[key]))
		}
	default:
		w.walkReflectStringLeaves(path, raw, base)
	}
}

func (w *referenceFieldWalker) walkReflectStringLeaves(path string, raw any, base ReferenceField) {
	rv := reflect.ValueOf(raw)
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			w.walkStringLeaves(fmt.Sprintf("%s[%d]", path, i), rv.Index(i).Interface(), base)
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
			w.walkStringLeaves(path+"."+key, rv.MapIndex(reflect.ValueOf(key)).Interface(), base)
		}
	}
}

func sortedStringKeys[M ~map[string]V, V any](m M) []string {
	return slices.Sorted(maps.Keys(m))
}

func (f ReferenceField) withPathValue(path, value string) ReferenceField {
	f.Path = path
	f.Value = value
	return f
}

func (f ReferenceField) withMode(mode cmnvalue.Mode) ReferenceField {
	f.Mode = mode
	return f
}
