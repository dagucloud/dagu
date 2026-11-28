// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chat provides an executor for LLM-based session steps.
package chat

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/itchyny/gojq"
)

// toolParam represents a parsed parameter definition.
type toolParam = toolschema.Param

// ToolRegistry manages tool DAGs and converts them to LLM tool format.
type ToolRegistry struct {
	// tools maps tool name (from DAG.Name) to tool info
	tools map[string]*toolInfo
	// dagNames maps tool name to original DAG name (for lookup)
	dagNames map[string]string
}

// toolInfo contains the parsed tool information from a DAG.
type toolInfo struct {
	Name        string
	Description string
	DAG         *ir.DAG
	Params      []toolParam
}

// NewToolRegistry creates a registry for the named tool DAGs.
func NewToolRegistry(ctx context.Context, dagNames []string) (*ToolRegistry, error) {
	if len(dagNames) == 0 {
		return nil, nil
	}

	rCtx := runctx.GetContext(ctx)

	registry := &ToolRegistry{
		tools:    make(map[string]*toolInfo),
		dagNames: make(map[string]string),
	}

	for _, dagName := range dagNames {
		var dag *ir.DAG

		// First, check if it's a local DAG defined in the same file (using --- separator)
		// This follows the same pattern as SubDAGExecutor.NewSubDAGExecutor()
		if rCtx.DAG != nil && rCtx.DAG.LocalDAGs != nil {
			if localDAG, ok := rCtx.DAG.LocalDAGs[dagName]; ok {
				dag = localDAG
			}
		}

		// Load the named DAG when no inline definition matched.
		if dag == nil {
			if rCtx.DAGLoader == nil {
				return nil, fmt.Errorf("loading non-local tool DAG %q requires DAGLoader", dagName)
			}
			var err error
			dag, err = rCtx.DAGLoader.GetDAG(ctx, dagName)
			if err != nil {
				return nil, fmt.Errorf("failed to load tool DAG %q: %w", dagName, err)
			}
		}

		// Use DAG.Name as the tool name (this is what the LLM will use)
		toolName := dag.Name
		if toolName == "" {
			toolName = dagName // Fallback to filename-based name
		}

		params := toolschema.ParamsFromDefs(dag.ParamDefs)
		if len(params) == 0 {
			var err error
			params, err = toolschema.ParseParams(dag.DefaultParams)
			if err != nil {
				return nil, fmt.Errorf("failed to parse params for tool DAG %q: %w", dagName, err)
			}
		}
		if err := checkModelInputs(ctx, dag, params); err != nil {
			return nil, err
		}

		info := &toolInfo{
			Name:        toolName,
			Description: dag.Description,
			DAG:         dag,
			Params:      params,
		}

		registry.tools[toolName] = info
		registry.dagNames[toolName] = dagName
	}

	return registry, nil
}

// ToLLMTools converts the registry to LLM tool format.
func (r *ToolRegistry) ToLLMTools() []llmpkg.Tool {
	if r == nil {
		return nil
	}

	tools := make([]llmpkg.Tool, 0, len(r.tools))
	for _, info := range r.tools {
		tools = append(tools, llmpkg.Tool{
			Type: "function",
			Function: llmpkg.ToolFunction{
				Name:        info.Name,
				Description: info.Description,
				Parameters:  toolschema.Build(info.Params),
			},
		})
	}

	return tools
}

// GetDAGByToolName returns the DAG for a given tool name.
func (r *ToolRegistry) GetDAGByToolName(toolName string) (*ir.DAG, bool) {
	if r == nil {
		return nil, false
	}
	info, ok := r.tools[toolName]
	if !ok {
		return nil, false
	}
	return info.DAG, true
}

// GetDAGName returns the original DAG name for a tool name.
func (r *ToolRegistry) GetDAGName(toolName string) (string, bool) {
	if r == nil {
		return "", false
	}
	name, ok := r.dagNames[toolName]
	return name, ok
}

// HasTools returns true if any tools are registered.
func (r *ToolRegistry) HasTools() bool {
	return r != nil && len(r.tools) > 0
}

// substitutedRef matches the references that Dagu replaces with their value in
// the text itself: ${params}, ${params.NAME} and ${env.NAME}.
var substitutedRef = regexp.MustCompile(`\$\{\s*(?:params|(?:params|env)\.([A-Za-z_][A-Za-z0-9_]*))\s*\}`)

// quotedRef matches "${NAME}", which Dagu fills with a quoted value in a
// here-document body or a single-quoted span of a command.
var quotedRef = regexp.MustCompile(`"\$\{([A-Za-z_][A-Za-z0-9_]*)\}"`)

// pathRef matches ${NAME.path} and $NAME.path, which Dagu fills with a field of
// the JSON value of NAME on every shell when the path parses as jq.
var pathRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(\.[^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)(\.\S+)`)

// foreachNamespace stands for the item scope of a foreach body, which holds
// text that the model sets when the items or the key read an input.
const foreachNamespace = "foreach"

// envRef matches $NAME and ${NAME}, which Dagu also expands in an eval.
var envRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

// checkModelInputs fails when the tool DAG would evaluate text that the model
// sets: a command that Dagu fills with an input before the shell runs it, or an
// eval that reads an input. An env entry whose value holds an input counts as
// an input too.
func checkModelInputs(ctx context.Context, dag *ir.DAG, params []toolParam) error {
	if len(params) == 0 {
		return nil
	}
	inputs := make(map[string]bool, len(params)+2)
	for _, p := range params {
		inputs[p.Name] = true
	}
	// Both variables hold every param as JSON.
	inputs[runenv.EnvKeyDAGParamsJSON] = true
	inputs[runenv.EnvKeyDAGParamsJSONCompat] = true
	raw, err := rawDAG(ctx, dag)
	if err != nil {
		return err
	}
	inputs = withEnvCopies(inputs, raw.Env)

	for _, def := range raw.ParamDefs {
		if ref, variable, ok := modelRef(def.Eval, inputs, true); ok {
			return modelInputError(dag, fmt.Sprintf("the eval of param %q", def.Name), ref, variable, false)
		}
	}
	if err := checkConditions(dag, "a precondition", dag.Preconditions, inputs); err != nil {
		return err
	}
	type pending struct {
		step   *ir.Step
		inputs map[string]bool
	}
	h := dag.HandlerOn
	var queue []pending
	for _, step := range []*ir.Step{h.Init, h.Success, h.Failure, h.Abort, h.Exit, h.Wait} {
		queue = append(queue, pending{step, inputs})
	}
	for i := range dag.Steps {
		queue = append(queue, pending{&dag.Steps[i], inputs})
	}
	for len(queue) > 0 {
		step := queue[0].step
		stepInputs := queue[0].inputs
		queue = queue[1:]
		if step == nil {
			continue
		}
		stepInputs = withEnvCopies(stepInputs, step.Env)
		shell, local := localShell(ctx, dag, step)
		// Dagu fills $NAME into the command of a local command step unless a
		// POSIX or Nix shell runs it.
		fills := local && !cmdutil.IsUnixLikeShell(shell) && !cmdutil.IsNixShell(shell)
		where := fmt.Sprintf("the command of step %q", step.Name)
		for _, text := range commandTexts(step, local && shell != "") {
			if ref, variable, ok := modelRef(text, stepInputs, fills); ok {
				return modelInputError(dag, where, ref, variable, !fills)
			}
		}
		if err := checkConditions(dag, fmt.Sprintf("a precondition of step %q", step.Name), step.Preconditions, stepInputs); err != nil {
			return err
		}
		if step.Foreach != nil {
			// Dagu writes each item into the body as ${foreach.<as>}. The body
			// sees only this foreach's item scope, not that of an outer one.
			bodyInputs := stepInputs
			if reads := foreachReadsInput(step.Foreach, stepInputs); reads != stepInputs[foreachNamespace] {
				bodyInputs = maps.Clone(stepInputs)
				bodyInputs[foreachNamespace] = reads
			}
			for i := range step.Foreach.Steps {
				queue = append(queue, pending{&step.Foreach.Steps[i], bodyInputs})
			}
		}
	}
	return nil
}

// rawDAG returns the tool DAG as its YAML defines it, which the child run
// loads. The loaded DAG holds its env with the param defaults filled in, which
// hides a copy of an input, and a DAG read from a run snapshot has no param
// evals.
func rawDAG(ctx context.Context, dag *ir.DAG) (*ir.DAG, error) {
	if len(dag.YamlData) == 0 {
		return dag, nil
	}
	// Only the env and the param evals are read, so the params schema is
	// skipped. The source file keeps relative paths resolving as they did when
	// the DAG loaded.
	opts := []spec.LoadOption{
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
		spec.WithSourceFile(cmp.Or(dag.SourceFile, dag.Location)),
	}
	if len(dag.BaseConfigData) > 0 {
		opts = append(opts, spec.WithBaseConfigContent(dag.BaseConfigData))
	}
	raw, err := spec.LoadYAML(ctx, dag.YamlData, opts...)
	if err != nil {
		return nil, fmt.Errorf("tool DAG %q: failed to read its definition: %w", dag.Name, err)
	}
	return raw, nil
}

// foreachReadsInput reports whether the items or the item key of a foreach
// read an input. Dagu resolves the strings of static items too.
func foreachReadsInput(foreach *ir.ForeachConfig, inputs map[string]bool) bool {
	texts := appendStrings([]string{foreach.ItemsExpr, foreach.Key}, foreach.Items)
	return slices.ContainsFunc(texts, func(text string) bool {
		_, _, found := modelRef(text, inputs, true)
		return found
	})
}

// appendStrings appends the strings that value holds at any depth.
func appendStrings(texts []string, value any) []string {
	switch v := value.(type) {
	case string:
		texts = append(texts, v)
	case []any:
		for _, item := range v {
			texts = appendStrings(texts, item)
		}
	case map[string]any:
		for _, item := range v {
			texts = appendStrings(texts, item)
		}
	}
	return texts
}

// withEnvCopies returns inputs with the names of the env entries whose value
// reads an input, since Dagu copies the input into such an entry.
func withEnvCopies(inputs map[string]bool, env []string) map[string]bool {
	copied := false
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || inputs[name] {
			continue
		}
		if _, _, found := modelRef(value, inputs, true); !found {
			continue
		}
		if !copied {
			inputs = maps.Clone(inputs)
			copied = true
		}
		inputs[name] = true
	}
	return inputs
}

// localShell returns the shell that runs the command of a local command step:
// the step's, else the DAG's, else the default one. It reports false for a
// step of another executor.
func localShell(ctx context.Context, dag *ir.DAG, step *ir.Step) (string, bool) {
	if !step.ExecutorConfig.IsCommand() && step.ExecutorConfig.Type != "shell" {
		return "", false
	}
	shell := step.Shell
	if shell == "" {
		shell = dag.Shell
	}
	if shell == "" {
		shell = cmdutil.GetShellCommand(config.GetConfig(ctx).Core.DefaultShell)
	}
	return shell, true
}

// checkConditions checks the evals of conditions, and the conditions that run
// as a command. Dagu fills $NAME into a condition command too.
func checkConditions(dag *ir.DAG, where string, conditions []*ir.Condition, inputs map[string]bool) error {
	for _, c := range conditions {
		if c == nil {
			continue
		}
		if ref, variable, ok := modelRef(c.Eval, inputs, true); ok {
			return modelInputError(dag, where, ref, variable, false)
		}
		if c.ExpectedPatterns() != nil {
			// A value match compares the text and runs nothing.
			continue
		}
		if ref, variable, ok := modelRef(c.Condition, inputs, true); ok {
			return modelInputError(dag, where, ref, variable, false)
		}
	}
	return nil
}

// commandTexts returns the texts of a step that become a command or a script.
// When a shell runs the command text of an entry and the step has no script,
// the split command and args of the entry never run, and their quotes are
// gone, so they are left out.
func commandTexts(step *ir.Step, shellConfigured bool) []string {
	texts := []string{step.Command, step.CmdWithArgs, step.CmdArgsSys, step.ShellCmdArgs, step.Script}
	texts = append(texts, step.Args...)
	for _, entry := range step.Commands {
		texts = append(texts, entry.CmdWithArgs)
		if shellConfigured && entry.CmdWithArgs != "" && step.Script == "" {
			continue
		}
		texts = append(texts, entry.Command)
		texts = append(texts, entry.Args...)
	}
	return texts
}

// modelRef returns the first reference in text that reads an input of the
// model, and the variable that holds the input. In an eval, $NAME and ${NAME}
// count too.
func modelRef(text string, inputs map[string]bool, eval bool) (ref, variable string, found bool) {
	for _, m := range substitutedRef.FindAllStringSubmatch(text, -1) {
		if m[1] == "" {
			return m[0], runenv.EnvKeyDAGParamsJSON, true
		}
		if inputs[m[1]] {
			return m[0], m[1], true
		}
	}
	for _, m := range quotedRef.FindAllStringSubmatch(text, -1) {
		if inputs[m[1]] {
			return m[0], m[1], true
		}
	}
	for _, m := range pathRef.FindAllStringSubmatch(text, -1) {
		if name := m[1] + m[3]; inputs[name] && parsesAsPath(m[2]+m[4]) {
			return m[0], name, true
		}
	}
	if !eval {
		return "", "", false
	}
	for _, m := range envRef.FindAllStringSubmatch(text, -1) {
		if inputs[m[1]] {
			return m[0], m[1], true
		}
	}
	return "", "", false
}

// parsesAsPath reports whether Dagu reads path as a JSON path. Dagu leaves a
// reference whose path does not parse as jq to the shell as written.
func parsesAsPath(path string) bool {
	_, err := gojq.Parse(path)
	return err == nil
}

// modelInputError names the tool DAG, the place and the reference. A command
// run by a POSIX or Nix shell can read the input from its variable instead,
// which the shell keeps as data. No variable holds a foreach item, so the step
// copies it into one.
func modelInputError(dag *ir.DAG, where, ref, variable string, hint bool) error {
	err := fmt.Errorf("tool DAG %q: %s refers to %s, which holds text that the model sets, and that text would run as code", dag.Name, where, ref)
	switch {
	case !hint:
		return err
	case variable == foreachNamespace:
		return fmt.Errorf("%w; copy it into an env entry of the step, such as VALUE: %s, and read \"$VALUE\" in the command, which the shell keeps as data", err, ref)
	default:
		return fmt.Errorf("%w; read it as \"$%s\" in the command, which the shell keeps as data", err, variable)
	}
}
