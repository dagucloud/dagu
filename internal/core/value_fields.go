// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
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
	noticePath    string
	OwnerStepName string
	OwnerStepID   string
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

func (f ReferenceField) noticeFieldPath() string {
	if f.noticePath != "" {
		return f.noticePath
	}
	return f.Path
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
		OwnerStepID:   step.ID,
		Field:         cmnvalue.WorkflowField(""),
	}
	command := step.CommandResolution(context.Background())
	scriptCommand := step.ScriptResolution(context.Background())

	w.add(base.withPathValue(path+".run", step.Script).withField(scriptReferenceField(path+".run", step, scriptCommand)))
	w.add(base.withPathValue(path+".command", step.Command).withField(cmnvalue.DirectCommandField(path+".command", command)))
	w.add(base.withPathValue(path+".cmd_with_args", step.CmdWithArgs).withField(cmnvalue.ShellCommandField(path+".cmd_with_args", command)))
	w.add(base.withPathValue(path+".cmd_args_sys", step.CmdArgsSys).withField(cmnvalue.DirectCommandField(path+".cmd_args_sys", command)))
	w.add(base.withPathValue(path+".shell_cmd_args", step.ShellCmdArgs).withField(cmnvalue.ShellCommandField(path+".shell_cmd_args", command)))
	w.add(base.withPathValue(path+".shell", step.Shell).withField(cmnvalue.StepShellField(path + ".shell")))
	for i, arg := range step.Args {
		fieldPath := fmt.Sprintf("%s.args[%d]", path, i)
		w.add(base.withPathValue(fieldPath, arg).withField(cmnvalue.DirectCommandField(fieldPath, command)))
	}
	for i, arg := range step.ShellArgs {
		fieldPath := fmt.Sprintf("%s.shell_args[%d]", path, i)
		w.add(base.withPathValue(fieldPath, arg).withField(cmnvalue.StepShellField(fieldPath)))
	}
	for i, cmd := range step.Commands {
		noticePath := commandEntryNoticePath(path, i, len(step.Commands))
		commandPath := fmt.Sprintf("%s.run[%d].command", path, i)
		w.add(base.withPathValue(commandPath, cmd.Command).
			withNoticePath(noticePath).
			withField(cmnvalue.DirectCommandField(commandPath, command)))
		cmdWithArgsPath := fmt.Sprintf("%s.run[%d].cmd_with_args", path, i)
		w.add(base.withPathValue(cmdWithArgsPath, cmd.CmdWithArgs).
			withNoticePath(noticePath).
			withField(cmnvalue.ShellCommandField(cmdWithArgsPath, command)))
		for j, arg := range cmd.Args {
			argPath := fmt.Sprintf("%s.run[%d].args[%d]", path, i, j)
			w.add(base.withPathValue(argPath, arg).
				withNoticePath(noticePath).
				withField(cmnvalue.DirectCommandField(argPath, command)))
		}
	}

	w.walkStringLeaves(path+".with", step.ExecutorConfig.Config, base.withField(cmnvalue.ExecutorConfigField(path+".with")))
	w.add(base.withPathValue(path+".working_dir", step.Dir).withField(cmnvalue.StepDirField(path + ".working_dir")))
	w.walkEnvWith(path+".env", step.Env, base, cmnvalue.StepEnvField)
	w.walkConditions(path+".preconditions", step.Preconditions, base)
	w.walkRetryPolicy(path+".retry_policy", step.RetryPolicy, base)
	w.walkRepeatPolicy(path+".repeat_policy", step.RepeatPolicy, base)
	if step.RepeatPolicy.Condition != nil {
		fieldPath := path + ".repeat_policy.condition"
		w.add(base.withPathValue(fieldPath, step.RepeatPolicy.Condition.Condition).withField(cmnvalue.ConditionValueField(fieldPath)))
	}
	w.walkSubDAG(path+".child_dag", step.SubDAG, base)
	if step.Parallel != nil {
		w.walkParallel(path+".parallel", step.Parallel, base)
	}
	if step.Foreach != nil {
		w.walkForeach(path+".foreach", step.Foreach, base)
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
	w.walkLLM(path+".llm", step.LLM, base)
	w.walkMessages(path+".messages", step.Messages, base)
}

func scriptReferenceField(path string, step Step, command cmnvalue.CommandContext) cmnvalue.Field {
	if step.ExecutorConfig.Type == "template" {
		return cmnvalue.TemplateScriptField(path)
	}
	if step.ExecutorConfig.IsCommand() {
		return cmnvalue.CommandScriptField(path, command)
	}
	return cmnvalue.ShellCommandField(path, command)
}

func commandEntryNoticePath(stepPath string, index, count int) string {
	if count == 1 {
		return stepPath + ".run"
	}
	return fmt.Sprintf("%s.run[%d]", stepPath, index)
}

func (w *referenceFieldWalker) walkRetryPolicy(path string, policy RetryPolicy, base ReferenceField) {
	w.add(base.withPathValue(path+".limit", policy.LimitStr).withField(cmnvalue.RetryIntegerField(path + ".limit")))
	w.add(base.withPathValue(path+".interval_sec", policy.IntervalSecStr).withField(cmnvalue.RetryIntegerField(path + ".interval_sec")))
}

func (w *referenceFieldWalker) walkRepeatPolicy(path string, policy RepeatPolicy, base ReferenceField) {
	w.add(base.withPathValue(path+".limit", policy.LimitStr).withField(cmnvalue.RepeatIntegerField(path + ".limit")))
	w.add(base.withPathValue(path+".interval_sec", policy.IntervalStr).withField(cmnvalue.RepeatIntegerField(path + ".interval_sec")))
	w.add(base.withPathValue(path+".max_interval_sec", policy.MaxIntervalStr).withField(cmnvalue.RepeatIntegerField(path + ".max_interval_sec")))
}

func (w *referenceFieldWalker) walkSubDAG(path string, subDAG *SubDAG, base ReferenceField) {
	if subDAG == nil {
		return
	}
	w.add(base.withPathValue(path+".name", subDAG.Name).withField(cmnvalue.SubDAGNameField(path + ".name")))
	w.add(base.withPathValue(path+".params", subDAG.Params).withField(cmnvalue.SubDAGParamsField(path + ".params")))
}

func (w *referenceFieldWalker) walkLLM(path string, llm *LLMConfig, base ReferenceField) {
	if llm == nil {
		return
	}
	w.add(base.withPathValue(path+".system", llm.System).withField(cmnvalue.WorkflowField(path + ".system")))
	w.add(base.withPathValue(path+".base_url", llm.BaseURL).withField(cmnvalue.ExecutorConfigField(path + ".base_url")))
	for i, model := range llm.Models {
		modelPath := fmt.Sprintf("%s.model[%d]", path, i)
		w.add(base.withPathValue(modelPath+".base_url", model.BaseURL).withField(cmnvalue.ExecutorConfigField(modelPath + ".base_url")))
	}
}

func (w *referenceFieldWalker) walkMessages(path string, messages []LLMMessage, base ReferenceField) {
	for i, message := range messages {
		fieldPath := fmt.Sprintf("%s[%d].content", path, i)
		w.add(base.withPathValue(fieldPath, message.Content).withField(cmnvalue.WorkflowField(fieldPath)))
	}
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

func (w *referenceFieldWalker) walkForeach(path string, foreach *ForeachConfig, base ReferenceField) {
	w.add(base.withPathValue(path+".items", foreach.ItemsExpr).withField(cmnvalue.WorkflowField(path + ".items")))
	w.walkStringLeaves(path+".items", foreach.Items, base.withField(cmnvalue.WorkflowField(path+".items")))
	w.add(base.withPathValue(path+".key", foreach.Key).withField(cmnvalue.WorkflowField(path + ".key")))
	for i, step := range foreach.Steps {
		w.walkStep(fmt.Sprintf("%s.steps[%d]", path, i), step)
	}
	for _, key := range sortedStringKeys(foreach.Collect) {
		fieldPath := path + ".collect." + key
		w.add(base.withPathValue(fieldPath, foreach.Collect[key]).withField(cmnvalue.WorkflowField(fieldPath)))
	}
}

func (w *referenceFieldWalker) walkStdoutOutputs(path string, outputs *StepOutputsConfig, base ReferenceField) {
	for _, key := range sortedStringKeys(outputs.Fields) {
		entry := outputs.Fields[key]
		if entry.HasValue {
			fieldPath := path + ".fields." + key + ".value"
			w.walkStringLeaves(fieldPath, entry.Value, base.withField(cmnvalue.StructuredOutputLiteralField(fieldPath)))
		}
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

func (f ReferenceField) withNoticePath(path string) ReferenceField {
	f.noticePath = path
	return f
}
