// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// Responses to a model provider asking a person to confirm actions.
const (
	confirmationFail  = "fail"
	confirmationAllow = "allow"
)

const (
	defaultOperationTimeout = 5 * time.Minute
	defaultAskTimeout       = time.Hour
	defaultMaxActions       = 50
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
	Cache          *bool             `json:"cache,omitempty"`
	MaxActions     int               `json:"max_actions,omitempty"`
	OnConfirmation string            `json:"on_confirmation,omitempty"`
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
	Cache       *bool  `json:"cache,omitempty"`
	MaxActions  int    `json:"max_actions,omitempty"`
}

type extractSpec struct {
	Instruction string         `json:"instruction"`
	Schema      map[string]any `json:"schema"`
}

type askSpec struct {
	Prompt  string `json:"prompt"`
	As      string `json:"as"`
	Timeout string `json:"timeout,omitempty"`
}

// condition is the value of expect or when: a statement the model judges
// against the screen, optionally rechecked until within passes.
type condition struct {
	Statement string `json:"statement"`
	// Within is how long the statement is rechecked before it is taken as
	// false, such as 30s.
	Within string `json:"within,omitempty"`
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

// UnmarshalJSON accepts a statement string or an object.
func (c *condition) UnmarshalJSON(data []byte) error {
	var statement string
	if err := json.Unmarshal(data, &statement); err == nil {
		c.Statement = statement
		return nil
	}
	type plain condition
	return json.Unmarshal(data, (*plain)(c))
}

// window returns how long the statement is rechecked, or zero to check
// once.
func (c condition) window() time.Duration {
	d, _ := time.ParseDuration(c.Within)
	return max(d, 0)
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
	if o.When != nil {
		texts = append(texts, o.When.Statement)
	}
	switch {
	case o.Act != nil:
		texts = append(texts, o.Act.Instruction)
	case o.Extract != nil:
		texts = append(texts, o.Extract.Instruction)
	case o.Expect != nil:
		texts = append(texts, o.Expect.Statement)
	case o.Ask != nil:
		texts = append(texts, o.Ask.Prompt)
	}
	return texts
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

func (c config) cacheEnabled() bool {
	return c.Cache == nil || *c.Cache
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

var errNoModel = errors.New("computer actions need a model: set llm at the DAG level or with.llm on the step")

func validateStep(step ir.Step) error {
	if step.LLM == nil {
		return errNoModel
	}
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	return cfg.validateModels(step.LLM.GetModels())
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
		if op.Act != nil {
			for _, name := range agentstep.VariableReferences(op.Act.Instruction) {
				_, isVariable := c.Variables[name]
				_, isEarlierAsk := asks[name]
				if !isVariable && !isEarlierAsk {
					return fmt.Errorf("computer: do[%d]: act references %%%s%%, which is not in with.variables or an earlier ask", i, name)
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

func (c condition) validate() error {
	if strings.TrimSpace(c.Statement) == "" {
		return errors.New("statement must not be empty")
	}
	return agentstep.ValidateDuration("within", c.Within)
}

func positiveInteger() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Minimum: new(1.0)}
}

// conditionSchema accepts a statement or an object with a statement.
func conditionSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Types:                []string{"string", "object"},
		MinLength:            new(1),
		AdditionalProperties: agentstep.NoExtraProperties(),
		Required:             []string{"statement"},
		Properties: map[string]*jsonschema.Schema{
			"statement": agentstep.NonEmptyString(),
			"within":    agentstep.StringSchema(),
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
		"cache":           {Type: "boolean"},
		"max_actions":     positiveInteger(),
		"on_confirmation": {Type: "string", Enum: []any{confirmationFail, confirmationAllow}},
		"do":              {Type: "array", MinItems: new(1), Items: operationSchema},
	},
}
