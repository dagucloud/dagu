// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const executorType = ir.ExecutorTypeComputer

// Operation kinds accepted in with.do.
const (
	opLaunch     = "launch"
	opAct        = "act"
	opExtract    = "extract"
	opExpect     = "expect"
	opWait       = "wait"
	opScreenshot = "screenshot"
	opAsk        = "ask"
)

// Automatic screenshot policies.
const (
	// screenshotsOnFailure captures the screen only when the step fails.
	screenshotsOnFailure = "on_failure"
	// screenshotsFinal also captures the screen when the step succeeds.
	screenshotsFinal = "final"
	// screenshotsEach also captures the screen after every operation.
	screenshotsEach  = "each"
	screenshotsNever = "never"
)

// How much AI decides for an act or an extract: the model every run, the
// model only when a replay misses, or never.
const (
	aiEveryRun = "every_run"
	aiOnMiss   = "on_miss"
	aiNever    = "never"
)

// Responses to a model provider asking a person to confirm actions.
const (
	confirmationFail  = "fail"
	confirmationAllow = "allow"
)

const (
	defaultOperationTimeout = 5 * time.Minute
	defaultAskTimeout       = time.Hour
	defaultMaxActions       = 50
	defaultIdle             = 15 * time.Second
)

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{LLM: true})
}

// config is the resolved with block of a computer step.
type config struct {
	Mode           string            `json:"mode,omitempty"`
	Variables      map[string]string `json:"variables,omitempty"`
	Screenshots    string            `json:"screenshots,omitempty"`
	AI             string            `json:"ai,omitempty"`
	Cache          *bool             `json:"cache,omitempty"`
	MaxActions     int               `json:"max_actions,omitempty"`
	OnConfirmation string            `json:"on_confirmation,omitempty"`
	Idle           string            `json:"idle,omitempty"`
	Do             []operation       `json:"do"`
}

// operation is one item of with.do. Exactly one operation field is set.
type operation struct {
	Launch     *launchSpec  `json:"launch,omitempty"`
	Act        *actSpec     `json:"act,omitempty"`
	Extract    *extractSpec `json:"extract,omitempty"`
	Expect     *condition   `json:"expect,omitempty"`
	Wait       string       `json:"wait,omitempty"`
	Screenshot string       `json:"screenshot,omitempty"`
	Ask        *askSpec     `json:"ask,omitempty"`
	When       *condition   `json:"when,omitempty"`
	Timeout    string       `json:"timeout,omitempty"`
}

type launchSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type actSpec struct {
	Instruction string `json:"instruction"`
	AI          string `json:"ai,omitempty"`
	Cache       *bool  `json:"cache,omitempty"`
	MaxActions  int    `json:"max_actions,omitempty"`
}

type extractSpec struct {
	Instruction string         `json:"instruction"`
	AI          string         `json:"ai,omitempty"`
	Schema      map[string]any `json:"schema"`
}

type askSpec struct {
	Prompt  string `json:"prompt"`
	As      string `json:"as"`
	Timeout string `json:"timeout,omitempty"`
}

// UnmarshalJSON accepts a command string or an object.
func (l *launchSpec) UnmarshalJSON(data []byte) error {
	var command string
	if err := json.Unmarshal(data, &command); err == nil {
		l.Command = command
		return nil
	}
	type plain launchSpec
	return json.Unmarshal(data, (*plain)(l))
}

// UnmarshalJSON accepts an instruction string or an object.
func (a *actSpec) UnmarshalJSON(data []byte) error {
	var instruction string
	if err := json.Unmarshal(data, &instruction); err == nil {
		a.Instruction = instruction
		return nil
	}
	type plain actSpec
	return json.Unmarshal(data, (*plain)(a))
}

// kind returns the operation name.
func (o operation) kind() string {
	switch {
	case o.Launch != nil:
		return opLaunch
	case o.Act != nil:
		return opAct
	case o.Extract != nil:
		return opExtract
	case o.Expect != nil:
		return opExpect
	case o.Wait != "":
		return opWait
	case o.Screenshot != "":
		return opScreenshot
	case o.Ask != nil:
		return opAsk
	default:
		return ""
	}
}

// operationTexts returns the text of each operation that reaches the model.
func (c config) operationTexts() []agentstep.OperationTexts {
	texts := make([]agentstep.OperationTexts, 0, len(c.Do))
	for _, op := range c.Do {
		texts = append(texts, agentstep.OperationTexts{Kind: op.kind(), Texts: op.promptTexts()})
	}
	return texts
}

// promptTexts returns the operation texts that reach the model.
func (o operation) promptTexts() []string {
	texts := make([]string, 0, 2)
	if o.When != nil && o.When.judged() {
		texts = append(texts, o.When.Statement)
	}
	switch {
	case o.Act != nil:
		texts = append(texts, o.Act.Instruction)
	case o.Extract != nil:
		texts = append(texts, o.Extract.Instruction)
	case o.Expect != nil && o.Expect.judged():
		texts = append(texts, o.Expect.Statement)
	case o.Ask != nil:
		texts = append(texts, o.Ask.Prompt)
	}
	return texts
}

// subject is what an operation's timeline event names it by.
func (o operation) subject() string {
	switch {
	case o.Launch != nil:
		return strings.Join(append([]string{o.Launch.Command}, o.Launch.Args...), " ")
	case o.Act != nil:
		return o.Act.Instruction
	case o.Extract != nil:
		return o.Extract.Instruction
	case o.Expect != nil:
		return o.Expect.String()
	case o.Wait != "":
		return o.Wait
	case o.Screenshot != "":
		return o.Screenshot
	case o.Ask != nil:
		return o.Ask.Prompt
	}
	return ""
}

func (o operation) timeout() time.Duration {
	if d, err := time.ParseDuration(o.Timeout); err == nil && d > 0 {
		return d
	}
	return defaultOperationTimeout
}

func (a askSpec) timeout() time.Duration {
	if d, err := time.ParseDuration(a.Timeout); err == nil && d > 0 {
		return d
	}
	return defaultAskTimeout
}

func (c config) mode() computeruse.Mode {
	if c.Mode == "" {
		return computeruse.ModeAuto
	}
	return computeruse.Mode(c.Mode)
}

// choice returns the step's choice of how much AI decides. cache: false,
// from before the choice existed, means every run.
func (c config) choice() string {
	switch {
	case c.AI != "":
		return c.AI
	case c.Cache != nil && !*c.Cache:
		return aiEveryRun
	default:
		return aiOnMiss
	}
}

// actChoice returns how much AI decides for an act: its own setting, or
// the step's.
func (c config) actChoice(spec actSpec) string {
	switch {
	case spec.AI != "":
		return spec.AI
	case spec.Cache != nil && !*spec.Cache:
		return aiEveryRun
	default:
		return c.choice()
	}
}

// extractChoice returns how much AI decides for an extract.
func (c config) extractChoice(spec extractSpec) string {
	if spec.AI != "" {
		return spec.AI
	}
	return c.choice()
}

// modelOperation returns the first operation that can call the model under
// its choice, which is what makes the step need one.
func (c config) modelOperation() (index int, kind string, found bool) {
	for i, op := range c.Do {
		switch {
		case op.When != nil && op.When.judged():
			return i, "when", true
		case op.Expect != nil && op.Expect.judged():
			return i, opExpect, true
		case op.Act != nil && c.actChoice(*op.Act) != aiNever:
			return i, opAct, true
		case op.Extract != nil && c.extractChoice(*op.Extract) != aiNever:
			return i, opExtract, true
		}
	}
	return 0, "", false
}

// idle returns how long nobody may have used the desktop before the step
// sends input; zero turns the wait off.
func (c config) idle() time.Duration {
	if c.Idle == "" {
		return defaultIdle
	}
	d, _ := time.ParseDuration(c.Idle)
	return d
}

// maxActions returns the action budget of an act.
func (c config) maxActions(spec actSpec) int {
	switch {
	case spec.MaxActions > 0:
		return spec.MaxActions
	case c.MaxActions > 0:
		return c.MaxActions
	default:
		return defaultMaxActions
	}
}

func (c config) screenshotPolicy() string {
	if c.Screenshots == "" {
		return screenshotsOnFailure
	}
	return c.Screenshots
}

// capturesFinalScreenshot reports whether a successful step saves a
// screenshot of the screen it ends on.
func (c config) capturesFinalScreenshot() bool {
	policy := c.screenshotPolicy()
	return policy == screenshotsFinal || policy == screenshotsEach
}

func parseConfig(raw map[string]any) (config, error) {
	var cfg config
	if err := registry.ValidateExecutorConfig(executorType, raw); err != nil {
		return cfg, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("computer configuration: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("computer configuration: %w", err)
	}
	return cfg, nil
}

func validateStep(step ir.Step) error {
	_, err := checkStep(step)
	return err
}

// checkStep parses and validates a step's configuration. A model is
// required only when some operation can call one.
func checkStep(step ir.Step) (config, error) {
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return cfg, err
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	if step.LLM == nil {
		if index, kind, found := cfg.modelOperation(); found {
			return cfg, fmt.Errorf("computer actions need a model for do[%d] %s: set llm at the DAG level or with.llm on the step", index, kind)
		}
		return cfg, nil
	}
	return cfg, cfg.validateModels(step.LLM.GetModels())
}

// validateModels rejects native mode for providers without a native
// computer-use tool. Providers set by reference resolve at run time.
func (c config) validateModels(models []ir.ModelEntry) error {
	if c.mode() != computeruse.ModeNative {
		return nil
	}
	for _, model := range models {
		if strings.Contains(model.Provider, "$") {
			continue
		}
		if !computeruse.HasNative(llmpkg.ProviderType(model.Provider)) {
			return fmt.Errorf("computer: provider %q has no native computer use; set mode to %q", model.Provider, computeruse.ModeGeneric)
		}
	}
	return nil
}

func (c config) validate() error {
	if len(c.Do) == 0 {
		return errors.New("computer: with.do must list at least one operation")
	}
	if c.AI != "" && c.Cache != nil {
		return errors.New("computer: set ai or cache, not both; cache: false means ai: every_run")
	}
	if c.Idle != "" && !strings.Contains(c.Idle, "$") {
		if d, err := time.ParseDuration(c.Idle); err != nil || d < 0 {
			return fmt.Errorf("computer: idle %q must be a duration such as 15s, or 0 to not wait", c.Idle)
		}
	}
	for name := range c.Variables {
		if !agentstep.IdentifierPattern.MatchString(name) {
			return fmt.Errorf("computer: variable name %q must match %s", name, agentstep.IdentifierPattern)
		}
	}
	outputs := make(map[string]int)
	asks := make(map[string]int)
	for i, op := range c.Do {
		if err := op.validate(); err != nil {
			return fmt.Errorf("computer: do[%d]: %w", i, err)
		}
		if err := c.validateChoice(op); err != nil {
			return fmt.Errorf("computer: do[%d]: %w", i, err)
		}
		references := map[string][]string{}
		if op.Act != nil {
			references[opAct] = agentstep.VariableReferences(op.Act.Instruction)
		}
		if op.When != nil {
			references["when"] = op.When.references()
		}
		if op.Expect != nil {
			references[opExpect] = op.Expect.references()
		}
		for kind, names := range references {
			for _, name := range names {
				_, isVariable := c.Variables[name]
				_, isEarlierAsk := asks[name]
				if !isVariable && !isEarlierAsk {
					return fmt.Errorf("computer: do[%d]: %s references %%%s%%, which is not in with.variables or an earlier ask", i, kind, name)
				}
			}
		}
		if op.Extract != nil {
			properties, _ := op.Extract.Schema["properties"].(map[string]any)
			for name := range properties {
				if first, exists := outputs[name]; exists {
					return fmt.Errorf("computer: do[%d]: output %q is already extracted by do[%d]", i, name, first)
				}
				outputs[name] = i
			}
		}
		if op.Ask != nil {
			if _, exists := c.Variables[op.Ask.As]; exists {
				return fmt.Errorf("computer: do[%d]: ask.as %q collides with a variable", i, op.Ask.As)
			}
			if first, exists := asks[op.Ask.As]; exists {
				return fmt.Errorf("computer: do[%d]: ask.as %q is already used by do[%d]", i, op.Ask.As, first)
			}
			asks[op.Ask.As] = i
		}
	}
	return nil
}

// validateChoice rejects an operation that cannot run under its choice of
// how much AI decides. A statement is a judgment only the model can make,
// and an extract has no form that reads the screen without one.
func (c config) validateChoice(o operation) error {
	if c.choice() == aiNever {
		if o.When != nil && o.When.judged() {
			return errors.New("when is judged by AI, which ai: never on the step does not allow; use an exact check: {text}, {element}, or {window}")
		}
		if o.Expect != nil && o.Expect.judged() {
			return errors.New("expect is judged by AI, which ai: never on the step does not allow; use an exact check: {text}, {element}, or {window}")
		}
	}
	switch {
	case o.Act != nil && o.Act.AI != "" && o.Act.Cache != nil:
		return errors.New("act: set ai or cache, not both; cache: false means ai: every_run")
	case o.Extract != nil && c.extractChoice(*o.Extract) == aiNever:
		return errors.New("extract needs AI; set ai: on_miss or ai: every_run on it")
	}
	return nil
}

func (o operation) validate() error {
	if o.kind() == "" {
		return errors.New("operation must set one of launch, act, extract, expect, wait, screenshot, or ask")
	}
	if err := agentstep.ValidateDuration("timeout", o.Timeout); err != nil {
		return err
	}
	if o.When != nil {
		if err := o.When.validate(); err != nil {
			return fmt.Errorf("when: %w", err)
		}
	}
	switch {
	case o.Launch != nil:
		if strings.TrimSpace(o.Launch.Command) == "" {
			return errors.New("launch command must not be empty")
		}
	case o.Act != nil:
		if strings.TrimSpace(o.Act.Instruction) == "" {
			return errors.New("act instruction must not be empty")
		}
	case o.Extract != nil:
		if strings.TrimSpace(o.Extract.Instruction) == "" {
			return errors.New("extract instruction must not be empty")
		}
		if o.Extract.Schema["type"] != "object" {
			return errors.New(`extract schema must have type: object`)
		}
	case o.Expect != nil:
		if err := o.Expect.validate(); err != nil {
			return fmt.Errorf("expect: %w", err)
		}
	case o.Wait != "":
		if err := agentstep.ValidateDuration("wait", o.Wait); err != nil {
			return err
		}
	case o.Screenshot != "":
		if !agentstep.FileNamePattern.MatchString(o.Screenshot) {
			return fmt.Errorf("screenshot name %q must match %s", o.Screenshot, agentstep.FileNamePattern)
		}
	case o.Ask != nil:
		if strings.TrimSpace(o.Ask.Prompt) == "" {
			return errors.New("ask prompt must not be empty")
		}
		if !agentstep.IdentifierPattern.MatchString(o.Ask.As) {
			return fmt.Errorf("ask.as %q must match %s", o.Ask.As, agentstep.IdentifierPattern)
		}
		if err := agentstep.ValidateDuration("ask.timeout", o.Ask.Timeout); err != nil {
			return err
		}
	}
	return nil
}

func positiveInteger() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Minimum: new(1.0)}
}

// aiSchema accepts a choice of how much AI decides.
func aiSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: []any{aiEveryRun, aiOnMiss, aiNever}}
}

// conditionSchema accepts a statement or an object with exactly one of a
// statement, a text, an element, or a window.
func conditionSchema() *jsonschema.Schema {
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		agentstep.NonEmptyString(),
		{
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Properties: map[string]*jsonschema.Schema{
				"statement": agentstep.NonEmptyString(),
				"text":      agentstep.NonEmptyString(),
				"element":   {Ref: "#/$defs/selector"},
				"window":    agentstep.NonEmptyString(),
				"within":    agentstep.StringSchema(),
			},
			OneOf: []*jsonschema.Schema{
				{Required: []string{"statement"}},
				{Required: []string{"text"}},
				{Required: []string{"element"}},
				{Required: []string{"window"}},
			},
		},
	}}
}

// selectorSchema accepts a selector as a condition names an element. A
// container is a selector too, through the definition the step's schema
// holds.
func selectorSchema() *jsonschema.Schema {
	roles := make([]any, 0, len(desktop.Roles))
	for _, role := range desktop.Roles {
		roles = append(roles, role)
	}
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Required:             []string{"role"},
		Properties: map[string]*jsonschema.Schema{
			"role":   {Type: "string", Enum: roles},
			"name":   agentstep.NonEmptyString(),
			"id":     agentstep.NonEmptyString(),
			"app":    agentstep.NonEmptyString(),
			"window": agentstep.NonEmptyString(),
			"in":     {Ref: "#/$defs/selector"},
			"near": {
				Type:                 "object",
				AdditionalProperties: agentstep.NoExtraProperties(),
				Required:             []string{"label", "side"},
				Properties: map[string]*jsonschema.Schema{
					"label": agentstep.NonEmptyString(),
					"side":  {Type: "string", Enum: []any{desktop.SideRight, desktop.SideBelow, desktop.SideLeft, desktop.SideAbove}},
				},
			},
			"nth": {Type: "integer", Minimum: new(0.0)},
		},
		AnyOf: []*jsonschema.Schema{
			{Required: []string{"name"}},
			{Required: []string{"id"}},
			{Required: []string{"near"}},
		},
	}
}

var operationSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: agentstep.NoExtraProperties(),
	Properties: map[string]*jsonschema.Schema{
		opLaunch: {
			Types:                []string{"string", "object"},
			MinLength:            new(1),
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"command"},
			Properties: map[string]*jsonschema.Schema{
				"command": agentstep.NonEmptyString(),
				"args":    {Type: "array", Items: agentstep.StringSchema()},
			},
		},
		opAct: {
			Types:                []string{"string", "object"},
			MinLength:            new(1),
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"instruction"},
			Properties: map[string]*jsonschema.Schema{
				"instruction": agentstep.NonEmptyString(),
				"ai":          aiSchema(),
				"cache":       {Type: "boolean"},
				"max_actions": positiveInteger(),
			},
		},
		opExtract: {
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"instruction", "schema"},
			Properties: map[string]*jsonschema.Schema{
				"instruction": agentstep.NonEmptyString(),
				"ai":          aiSchema(),
				"schema":      {Type: "object"},
			},
		},
		opExpect:     conditionSchema(),
		opWait:       agentstep.NonEmptyString(),
		opScreenshot: agentstep.NonEmptyString(),
		opAsk: {
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"prompt", "as"},
			Properties: map[string]*jsonschema.Schema{
				"prompt":  agentstep.NonEmptyString(),
				"as":      agentstep.NonEmptyString(),
				"timeout": agentstep.StringSchema(),
			},
		},
		"when":    conditionSchema(),
		"timeout": agentstep.StringSchema(),
	},
	OneOf: []*jsonschema.Schema{
		{Required: []string{opLaunch}},
		{Required: []string{opAct}},
		{Required: []string{opExtract}},
		{Required: []string{opExpect}},
		{Required: []string{opWait}},
		{Required: []string{opScreenshot}},
		{Required: []string{opAsk}},
	},
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: agentstep.NoExtraProperties(),
	Required:             []string{"do"},
	Properties: map[string]*jsonschema.Schema{
		"mode":            {Type: "string", Enum: []any{string(computeruse.ModeAuto), string(computeruse.ModeNative), string(computeruse.ModeGeneric)}},
		"variables":       {Type: "object", AdditionalProperties: agentstep.StringSchema()},
		"screenshots":     {Type: "string", Enum: []any{screenshotsOnFailure, screenshotsFinal, screenshotsEach, screenshotsNever}},
		"ai":              aiSchema(),
		"cache":           {Type: "boolean"},
		"max_actions":     positiveInteger(),
		"on_confirmation": {Type: "string", Enum: []any{confirmationFail, confirmationAllow}},
		"idle":            agentstep.StringSchema(),
		"do":              {Type: "array", MinItems: new(1), Items: operationSchema},
	},
	Defs: map[string]*jsonschema.Schema{"selector": selectorSchema()},
}
