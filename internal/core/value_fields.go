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
	Field         cmnvalue.Field
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
	root := ReferenceField{Field: cmnvalue.WorkflowField("")}
	w.walkEnvWith("env", dag.Env, root, cmnvalue.DAGEnvField)
	for i, dotenv := range dag.Dotenv {
		path := fmt.Sprintf("dotenv[%d]", i)
		w.add(root.withPathValue(path, dotenv).withField(cmnvalue.DotenvPathField(path)))
	}
	w.add(root.withPathValue("shell", dag.Shell).withField(cmnvalue.DAGShellField("shell")))
	for i, arg := range dag.ShellArgs {
		path := fmt.Sprintf("shell_args[%d]", i)
		w.add(root.withPathValue(path, arg).withField(cmnvalue.DAGShellField(path)))
	}
	w.add(root.withPathValue("working_dir", dag.WorkingDir).withField(cmnvalue.DAGWorkingDirField("working_dir")))
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
		Field:         cmnvalue.WorkflowField(""),
	}

	w.add(base.withPathValue(path+".run", step.Script).withField(cmnvalue.CommandScriptField(path+".run", cmnvalue.CommandContext{})))
	w.add(base.withPathValue(path+".command", step.Command).withField(cmnvalue.DirectCommandField(path+".command", cmnvalue.CommandContext{})))
	w.add(base.withPathValue(path+".cmd_with_args", step.CmdWithArgs).withField(cmnvalue.ShellCommandField(path+".cmd_with_args", cmnvalue.CommandContext{})))
	w.add(base.withPathValue(path+".cmd_args_sys", step.CmdArgsSys).withField(cmnvalue.DirectCommandField(path+".cmd_args_sys", cmnvalue.CommandContext{})))
	w.add(base.withPathValue(path+".shell_cmd_args", step.ShellCmdArgs).withField(cmnvalue.ShellCommandField(path+".shell_cmd_args", cmnvalue.CommandContext{})))
	w.add(base.withPathValue(path+".shell", step.Shell).withField(cmnvalue.StepShellField(path + ".shell")))
	for i, arg := range step.Args {
		fieldPath := fmt.Sprintf("%s.args[%d]", path, i)
		w.add(base.withPathValue(fieldPath, arg).withField(cmnvalue.DirectCommandField(fieldPath, cmnvalue.CommandContext{})))
	}
	for i, arg := range step.ShellArgs {
		fieldPath := fmt.Sprintf("%s.shell_args[%d]", path, i)
		w.add(base.withPathValue(fieldPath, arg).withField(cmnvalue.StepShellField(fieldPath)))
	}
	for i, cmd := range step.Commands {
		commandPath := fmt.Sprintf("%s.run[%d].command", path, i)
		w.add(base.withPathValue(commandPath, cmd.Command).withField(cmnvalue.DirectCommandField(commandPath, cmnvalue.CommandContext{})))
		cmdWithArgsPath := fmt.Sprintf("%s.run[%d].cmd_with_args", path, i)
		w.add(base.withPathValue(cmdWithArgsPath, cmd.CmdWithArgs).withField(cmnvalue.ShellCommandField(cmdWithArgsPath, cmnvalue.CommandContext{})))
		for j, arg := range cmd.Args {
			argPath := fmt.Sprintf("%s.run[%d].args[%d]", path, i, j)
			w.add(base.withPathValue(argPath, arg).withField(cmnvalue.DirectCommandField(argPath, cmnvalue.CommandContext{})))
		}
	}

	w.walkStringLeaves(path+".with", step.ExecutorConfig.Config, base.withField(cmnvalue.ExecutorConfigField(path+".with")))
	w.add(base.withPathValue(path+".working_dir", step.Dir).withField(cmnvalue.StepDirField(path + ".working_dir")))
	w.walkEnvWith(path+".env", step.Env, base, cmnvalue.WorkflowField)
	w.walkConditions(path+".preconditions", step.Preconditions, base)
	if step.RepeatPolicy.Condition != nil {
		fieldPath := path + ".repeat_policy.condition"
		w.add(base.withPathValue(fieldPath, step.RepeatPolicy.Condition.Condition).withField(cmnvalue.ConditionValueField(fieldPath)))
	}
	if step.Parallel != nil {
		w.walkParallel(path+".parallel", step.Parallel, base)
	}
	w.add(base.withPathValue(path+".stdout", step.Stdout).withField(cmnvalue.StepArtifactOutputField(path + ".stdout")))
	w.add(base.withPathValue(path+".stdout.artifact", step.StdoutArtifact).withField(cmnvalue.StepArtifactOutputField(path + ".stdout.artifact")))
	w.add(base.withPathValue(path+".stderr", step.Stderr).withField(cmnvalue.StepArtifactOutputField(path + ".stderr")))
	w.add(base.withPathValue(path+".stderr.artifact", step.StderrArtifact).withField(cmnvalue.StepArtifactOutputField(path + ".stderr.artifact")))
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
		fieldPath := fmt.Sprintf("%s[%d].condition", path, i)
		w.add(base.withPathValue(fieldPath, condition.Condition).withField(cmnvalue.ConditionValueField(fieldPath)))
	}
}

func (w *referenceFieldWalker) walkEnvWith(path string, env []string, base ReferenceField, fieldForPath func(string) cmnvalue.Field) {
	for i, entry := range env {
		_, value, ok := strings.Cut(entry, "=")
		if !ok {
			value = entry
		}
		fieldPath := fmt.Sprintf("%s[%d]", path, i)
		w.add(base.withPathValue(fieldPath, value).withField(fieldForPath(fieldPath)))
	}
}

func (w *referenceFieldWalker) walkParallel(path string, parallel *ParallelConfig, base ReferenceField) {
	w.add(base.withPathValue(path+".variable", parallel.Variable).withField(cmnvalue.ParallelItemField(path + ".variable")))
	for i, item := range parallel.Items {
		itemPath := fmt.Sprintf("%s.items[%d]", path, i)
		w.add(base.withPathValue(itemPath+".value", item.Value).withField(cmnvalue.ParallelItemField(itemPath + ".value")))
		for _, key := range sortedStringKeys(item.Params) {
			fieldPath := itemPath + ".params." + key
			w.add(base.withPathValue(fieldPath, item.Params[key]).withField(cmnvalue.ParallelItemParamField(fieldPath)))
		}
	}
}

func (w *referenceFieldWalker) walkStdoutOutputs(path string, outputs *StepOutputsConfig, base ReferenceField) {
	for _, key := range sortedStringKeys(outputs.Fields) {
		entry := outputs.Fields[key]
		if entry.HasValue {
			fieldPath := path + ".fields." + key + ".value"
			w.walkStringLeaves(fieldPath, entry.Value, base.withField(cmnvalue.StructuredOutputLiteralField(fieldPath)))
		}
		fieldPath := path + ".fields." + key + ".path"
		w.add(base.withPathValue(fieldPath, entry.Path).withField(cmnvalue.StructuredOutputPathField(fieldPath)))
	}
}

func (w *referenceFieldWalker) walkStructuredOutput(path string, output map[string]StepOutputEntry, base ReferenceField) {
	for _, key := range sortedStringKeys(output) {
		entry := output[key]
		if entry.HasValue {
			fieldPath := path + "." + key + ".value"
			w.walkStringLeaves(fieldPath, entry.Value, base.withField(cmnvalue.StructuredOutputLiteralField(fieldPath)))
		}
		fieldPath := path + "." + key + ".path"
		w.add(base.withPathValue(fieldPath, entry.Path).withField(cmnvalue.StructuredOutputPathField(fieldPath)))
	}
}

func (w *referenceFieldWalker) walkContainer(path string, container *Container, base ReferenceField) {
	if container == nil {
		return
	}
	w.add(base.withPathValue(path+".exec", container.Exec).withField(cmnvalue.ContainerField(path + ".exec")))
	w.add(base.withPathValue(path+".image", container.Image).withField(cmnvalue.ContainerField(path + ".image")))
	w.add(base.withPathValue(path+".name", container.Name).withField(cmnvalue.ContainerField(path + ".name")))
	w.add(base.withPathValue(path+".user", container.User).withField(cmnvalue.ContainerField(path + ".user")))
	w.add(base.withPathValue(path+".working_dir", container.WorkingDir).withField(cmnvalue.ContainerField(path + ".working_dir")))
	w.add(base.withPathValue(path+".network", container.Network).withField(cmnvalue.ContainerField(path + ".network")))
	for i, value := range container.Volumes {
		fieldPath := fmt.Sprintf("%s.volumes[%d]", path, i)
		w.add(base.withPathValue(fieldPath, value).withField(cmnvalue.ContainerField(fieldPath)))
	}
	for i, value := range container.Ports {
		fieldPath := fmt.Sprintf("%s.ports[%d]", path, i)
		w.add(base.withPathValue(fieldPath, value).withField(cmnvalue.ContainerField(fieldPath)))
	}
	w.walkEnvWith(path+".env", container.Env, base, cmnvalue.ContainerEnvField)
	for i, value := range container.Command {
		fieldPath := fmt.Sprintf("%s.command[%d]", path, i)
		w.add(base.withPathValue(fieldPath, value).withField(cmnvalue.DirectCommandField(fieldPath, cmnvalue.CommandContext{Target: cmnvalue.CommandTargetDocker})))
	}
	for i, value := range container.Shell {
		fieldPath := fmt.Sprintf("%s.shell[%d]", path, i)
		w.add(base.withPathValue(fieldPath, value).withField(cmnvalue.ShellCommandField(fieldPath, cmnvalue.CommandContext{Target: cmnvalue.CommandTargetDocker, ShellConfigured: true})))
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
	//nolint:exhaustive // Only collections can contain string leaves.
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
	if f.Field.Path() == "" {
		f.Field = cmnvalue.WorkflowField(path)
	}
	return f
}

func (f ReferenceField) withField(field cmnvalue.Field) ReferenceField {
	f.Field = field
	return f
}
