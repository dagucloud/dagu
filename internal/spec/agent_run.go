// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Spec 091 defines the action agent.run. The loader compiles a step with this
// action into harness.run and keeps an ir.AgentRun with the step.

// agentRunWithKeys lists the keys of the with of an agent.run step of the
// provider claude.
var agentRunWithKeys = []string{
	"prompt", "system", "provider", "endpoint", "dags", "tools", "skills",
	"max_tool_iterations", "max_tool_calls",
}

// agentRunOtherCLIKeys lists the keys of the with of a step of another CLI
// provider. Such a provider takes the model in the with. The provider claude
// takes it in the block endpoint.
var agentRunOtherCLIKeys = slices.Insert(slices.Clone(agentRunWithKeys), 3, "model")

// claudeProvider is the only CLI provider whose flags Spec 091 defines.
const claudeProvider = "claude"

// agentRunDraft carries the with of an agent.run step from the normalization
// of the action to the build of the step. Only the normalizer creates it, and
// it has no exported field, so a YAML key cannot fill it.
type agentRunDraft struct {
	with map[string]any
	ok   bool
}

// normalizeAgentRunAction keeps the with of an agent.run step. The checks need
// the build context, so the build of the step makes them.
func normalizeAgentRunAction(normalized map[string]any, with map[string]any) error {
	if with == nil {
		with = map[string]any{}
	}
	normalized["agent_run"] = &agentRunDraft{with: with, ok: true}
	delete(normalized, "with")
	delete(normalized, "action")
	return nil
}

// agentSource is the with of an agent.run step.
type agentSource struct {
	// label names the layer in errors, for example "with".
	label string
	raw   map[string]any
}

func (a agentSource) field(key string) string {
	return a.label + "." + key
}

func (a agentSource) has(key string) bool {
	_, ok := a.raw[key]
	return ok
}

// str reads a key that must be a non-empty string.
func (a agentSource) str(key string) (string, bool, error) {
	raw, present := a.raw[key]
	if !present {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", true, ir.NewValidationError(a.field(key), raw, fmt.Errorf("%s must be a non-empty string", key))
	}
	return strings.TrimSpace(text), true, nil
}

// text reads a key that must be a string. The string can be empty.
func (a agentSource) text(key string) (string, bool, error) {
	raw, present := a.raw[key]
	if !present {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", true, ir.NewValidationError(a.field(key), raw, fmt.Errorf("%s must be a string", key))
	}
	return text, true, nil
}

// integer reads a key that must be an integer of at least min.
func (a agentSource) integer(key string, minimum int) (int, bool, error) {
	raw, present := a.raw[key]
	if !present {
		return 0, false, nil
	}
	number, ok := asInt(raw)
	if !ok || number < minimum {
		return 0, true, ir.NewValidationError(a.field(key), raw,
			fmt.Errorf("%s must be an integer of at least %d", key, minimum))
	}
	return number, true, nil
}

// enum reads a key that must be one of the values.
func (a agentSource) enum(key string, values ...string) (string, bool, error) {
	raw, present := a.raw[key]
	if !present {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok || !slices.Contains(values, text) {
		return "", true, ir.NewValidationError(a.field(key), raw,
			fmt.Errorf("%s must be one of %s", key, strings.Join(values, ", ")))
	}
	return text, true, nil
}

// skillItems reads the key skills: a list of skill names. The skills load when
// the step compiles.
func (a agentSource) skillItems() ([]any, error) {
	raw, present := a.raw["skills"]
	if !present {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, ir.NewValidationError(a.field("skills"), raw, skillsListError(raw))
	}
	return items, nil
}

// notSupported builds the error of a key that the provider cannot run. The form
// is fixed by Spec 091.
func notSupported(field, key, provider, reason string, value any) error {
	return ir.NewValidationError(field, value,
		fmt.Errorf("key %q is not supported by the provider %q: %s", key, provider, reason))
}

// validateAgentRunSettings checks the type and the range of each key of Spec 091.
// The block endpoint of the provider claude is checked when the provider is
// selected: see selectAgentProvider.
func validateAgentRunSettings(src agentSource) error {
	for _, check := range []func() error{
		func() error { return validateAgentProvider(src) },
		func() error { _, _, err := src.str("model"); return err },
		func() error { _, _, err := src.str("prompt"); return err },
		func() error { _, _, err := src.text("system"); return err },
		func() error { _, _, err := src.integer("max_tool_iterations", 1); return err },
		func() error { _, _, err := src.integer("max_tool_calls", 1); return err },
		func() error { _, err := src.skillItems(); return err },
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// validateAgentProvider checks that the provider is a literal name. The route of
// a run depends on it, so a reference such as ${NAME} is not valid (Spec 091,
// "Provider").
func validateAgentProvider(src agentSource) error {
	provider, present, err := src.str("provider")
	if err != nil || !present {
		return err
	}
	if value.HasAnyReference(provider) {
		return ir.NewValidationError(src.field("provider"), provider,
			fmt.Errorf("provider must be a literal name, not a reference: Dagu routes the run when it loads the DAG"))
	}
	return nil
}

// checkAgentStepKeys checks the closed set of keys of the with of the step.
func checkAgentStepKeys(src agentSource, claude bool) error {
	valid := agentRunOtherCLIKeys
	if claude {
		valid = agentRunWithKeys
	}
	return checkClosedKeys(src.label, src.raw, "agent.run", valid)
}

// checkAgentProvider checks that the provider is a CLI provider (Spec 091,
// "Provider"): a harnesses entry of the DAG, except an ACP entry, or a built-in
// CLI provider that is not also a chat provider.
func checkAgentProvider(ctx stepBuildContext, provider string) error {
	var defs ir.HarnessDefinitions
	if ctx.dag != nil {
		defs = ctx.dag.Harnesses
	}
	if def, ok := defs[provider]; ok && def != nil {
		if def.Protocol == ir.HarnessProtocolACP {
			return fmt.Errorf("the provider %q is an ACP harness, which agent.run does not run", provider)
		}
		return nil
	}
	if ir.IsAgentCLIProvider(provider) {
		return nil
	}
	if _, err := llm.ParseProviderType(provider); err == nil {
		return fmt.Errorf("the provider %q is a chat provider, and agent.run runs a CLI provider; use chat.completion to call a model API", provider)
	}
	return fmt.Errorf("the provider %q is not a CLI provider or a harnesses entry; the CLI providers are %s",
		provider, strings.Join(ir.AgentCLIProviderNames(), ", "))
}

// isClaudeCLI reports whether the provider is the built-in CLI claude: the only
// provider that an endpoint serves, and so the only one that must name an endpoint
// and a model.
func isClaudeCLI(ctx stepBuildContext, provider string) bool {
	var defs ir.HarnessDefinitions
	if ctx.dag != nil {
		defs = ctx.dag.Harnesses
	}
	return provider == claudeProvider && ir.IsEffectiveBuiltinCLIHarnessProvider(provider, defs)
}

// agentSelection is the provider that applies to a step, and the model.
type agentSelection struct {
	provider string
	model    string
	// endpoint is the name of the AI endpoint. A step of the CLI claude always has
	// one.
	endpoint string
	// effort is the effort of the block endpoint. It is empty when the step has
	// none.
	effort string
}

// selectAgentProvider checks the provider of the step, and the block endpoint of
// the provider claude (Spec 091, "Endpoint"). The key provider is required.
func selectAgentProvider(ctx stepBuildContext, step agentSource) (agentSelection, error) {
	var selection agentSelection
	provider, present, err := step.str("provider")
	if err != nil {
		return selection, err
	}
	if !present {
		return selection, ir.NewValidationError(step.field("provider"), nil,
			fmt.Errorf("agent.run needs a provider; it names the CLI provider"))
	}
	if err := checkAgentProvider(ctx, provider); err != nil {
		return selection, ir.NewValidationError(step.field("provider"), provider, err)
	}
	selection.provider = provider
	if !isClaudeCLI(ctx, provider) {
		selection.model, _, err = step.str("model")
		return selection, err
	}
	raw, present := step.raw["endpoint"]
	if !present {
		return selection, ir.NewValidationError(step.field("endpoint"), nil,
			fmt.Errorf("agent.run needs an endpoint; add one on the AI page and name it here"))
	}
	block, err := readEndpointBlock(step.field("endpoint"), raw, "agent.run", true)
	if err != nil {
		return selection, err
	}
	selection.model = block.model
	selection.endpoint = block.name
	selection.effort = block.effort
	return selection, nil
}

// agentSettings is the value of the keys of the with that the step reads.
type agentSettings struct {
	prompt            string
	system            string
	maxToolIterations *int
	maxToolCalls      int
	// tools holds the entries of the keys dags and tools. It is empty when the
	// step gives none.
	tools      *toolMap
	skillItems []any
}

// secretReferenceIn returns the name of the first DAG secret that text refers
// to, in any form that Dagu expands, such as ${NAME:-word}.
func secretReferenceIn(ctx stepBuildContext, text string) (string, bool) {
	if ctx.dag == nil || len(ctx.dag.Secrets) == 0 {
		return "", false
	}
	for _, name := range value.EnvReferenceNames(text) {
		for _, secret := range ctx.dag.Secrets {
			if secret.Name == name {
				return name, true
			}
		}
	}
	return "", false
}

// readAgentSettings reads the keys of the with of the step.
func readAgentSettings(ctx stepBuildContext, step agentSource) (*agentSettings, error) {
	settings := &agentSettings{}

	prompt, ok, err := step.str("prompt")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ir.NewValidationError(step.field("prompt"), nil, fmt.Errorf("agent.run needs a prompt"))
	}
	settings.prompt = prompt
	settings.system, _, _ = step.text("system")

	for _, key := range []string{"prompt", "system"} {
		text, _ := step.raw[key].(string)
		if secret, found := secretReferenceIn(ctx, text); found {
			return nil, ir.NewValidationError(step.field(key), nil,
				fmt.Errorf("%s references the DAG secret %q; a secret must not enter a prompt or a system text", key, secret))
		}
	}

	if number, ok, _ := step.integer("max_tool_iterations", 1); ok {
		settings.maxToolIterations = &number
	}
	settings.maxToolCalls = ir.DefaultAgentMaxToolCalls
	if number, ok, _ := step.integer("max_tool_calls", 1); ok {
		settings.maxToolCalls = number
	}

	settings.tools, err = parseAgentTools(step.raw, step.label)
	if err != nil {
		return nil, err
	}
	if err := settings.tools.check(); err != nil {
		return nil, err
	}
	settings.skillItems, _ = step.skillItems()
	return settings, nil
}

// compileAgentRun checks an agent.run step and returns the step that it
// compiles to, with the compiled form to keep. The returned step runs as
// chat.completion or harness.run. The loader never changes the step it reads.
func compileAgentRun(ctx stepBuildContext, s *step) (*step, *ir.AgentRun, error) {
	draft := s.AgentRun
	if draft == nil || !draft.ok {
		return nil, nil, ir.NewValidationError("agent_run", nil,
			fmt.Errorf("agent_run is not a key of a step; write the step with action: agent.run"))
	}
	stepSrc := agentSource{label: "with", raw: draft.with}
	claude := false
	if provider, ok := stepSrc.raw["provider"].(string); ok {
		claude = isClaudeCLI(ctx, strings.TrimSpace(provider))
	}
	if err := checkAgentStepKeys(stepSrc, claude); err != nil {
		return nil, nil, err
	}
	if err := validateAgentRunSettings(stepSrc); err != nil {
		return nil, nil, err
	}

	selection, err := selectAgentProvider(ctx, stepSrc)
	if err != nil {
		return nil, nil, err
	}
	settings, err := readAgentSettings(ctx, stepSrc)
	if err != nil {
		return nil, nil, err
	}

	compiled := *s
	compiled.AgentRun = nil
	compiled.With = nil
	compiled.Config = nil

	if compiled.OutputSchema != nil {
		schema, ok := compiled.OutputSchema.(map[string]any)
		if !ok {
			return nil, nil, ir.NewValidationError("output_schema", compiled.OutputSchema,
				fmt.Errorf("output_schema must be an inline JSON Schema object"))
		}
		if err := validateAgentOutputSchema(schema); err != nil {
			return nil, nil, ir.NewValidationError("output_schema", nil, err)
		}
	}

	run := &ir.AgentRun{
		Provider:          selection.provider,
		Model:             selection.model,
		Endpoint:          selection.endpoint,
		Prompt:            settings.prompt,
		System:            settings.system,
		MaxToolIterations: settings.maxToolIterations,
		MaxToolCalls:      settings.maxToolCalls,
	}
	if err := compileAgentCLI(ctx, &compiled, stepSrc, settings, run, selection.effort); err != nil {
		return nil, nil, err
	}
	return &compiled, run, nil
}

// compileAgentCLI fills the step: a harness.run step.
func compileAgentCLI(ctx stepBuildContext, compiled *step, stepSrc agentSource, settings *agentSettings, run *ir.AgentRun, effort string) error {
	if compiled.Container != nil {
		return ir.NewValidationError("container", compiled.Container,
			fmt.Errorf("an agent.run step does not run in a container"))
	}

	isClaude := isClaudeCLI(ctx, run.Provider)

	config := map[string]any{"provider": run.Provider}
	if run.Model != "" {
		config["model"] = run.Model
	}

	if !isClaude {
		if err := checkAgentOtherCLIProvider(stepSrc, compiled, run); err != nil {
			return err
		}
	} else {
		if effort != "" {
			config["effort"] = effort
		}
		if err := compileClaudeKeys(ctx, settings, run); err != nil {
			return err
		}
	}
	compiled.Type = "harness"
	compiled.With = config
	return nil
}

// checkAgentOtherCLIProvider rejects the keys that a CLI provider other than
// claude cannot run. Such a provider supports model, prompt and timeout_sec
// only.
func checkAgentOtherCLIProvider(stepSrc agentSource, compiled *step, run *ir.AgentRun) error {
	if stepSrc.has("endpoint") {
		return notSupported(stepSrc.field("endpoint"), "endpoint", run.Provider,
			"the CLI claude is the only provider that an endpoint serves; Spec 094 maps the provider claude to the protocol anthropic", stepSrc.raw["endpoint"])
	}
	reason := fmt.Sprintf("the provider %q supports only model, prompt and timeout_sec; Spec 091 defines the flags of claude only", run.Provider)
	for _, key := range []string{"system", "dags", "tools", "skills", "max_tool_iterations", "max_tool_calls"} {
		if stepSrc.has(key) {
			return notSupported(stepSrc.field(key), key, run.Provider, reason, stepSrc.raw[key])
		}
	}
	if compiled.OutputSchema != nil {
		return notSupported("output_schema", "output_schema", run.Provider, reason, nil)
	}
	return nil
}

// compileClaudeKeys checks the keys of a step with the provider claude and
// fills the compiled form.
func compileClaudeKeys(ctx stepBuildContext, settings *agentSettings, run *ir.AgentRun) error {
	cli := &ir.AgentCLIConfig{}
	run.CLI = cli

	cli.Tools = settings.tools.irEntries()

	skills, err := parseSkillEntries(ctx.buildContext, settings.skillItems, "with.skills")
	if err != nil {
		return err
	}
	cli.Skills = skills
	return nil
}

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), v >= math.MinInt && v <= math.MaxInt
	case uint64:
		return int(v), v <= math.MaxInt
	default:
		return 0, false
	}
}
