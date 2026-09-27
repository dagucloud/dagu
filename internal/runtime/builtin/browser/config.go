// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/google/jsonschema-go/jsonschema"
)

const executorType = ir.ExecutorTypeBrowser

// Operation kinds accepted in with.do.
const (
	opGoto       = "goto"
	opAct        = "act"
	opExtract    = "extract"
	opExpect     = "expect"
	opWait       = "wait"
	opScreenshot = "screenshot"
	opAsk        = "ask"
)

// Automatic screenshot policies.
const (
	// screenshotsOnFailure captures the page only when the step fails.
	screenshotsOnFailure = "on_failure"
	// screenshotsFinal also captures the page when the step succeeds.
	screenshotsFinal = "final"
	// screenshotsEach also captures the page after every operation.
	screenshotsEach  = "each"
	screenshotsNever = "never"
)

const (
	defaultOperationTimeout = 2 * time.Minute
	defaultAskTimeout       = time.Hour
)

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{LLM: true})
}

// config is the resolved with block of a browser step.
type config struct {
	URL       string            `json:"url,omitempty"`
	Browser   browserOptions    `json:"browser"`
	Variables map[string]string `json:"variables,omitempty"`
	Cache     *bool             `json:"cache,omitempty"`
	Do        []operation       `json:"do"`
}

// browserOptions configures the browser a step launches.
type browserOptions struct {
	Headless       *bool     `json:"headless,omitempty"`
	Executable     string    `json:"executable,omitempty"`
	Viewport       *viewport `json:"viewport,omitempty"`
	Proxy          string    `json:"proxy,omitempty"`
	AllowedDomains []string  `json:"allowed_domains,omitempty"`
	Screenshots    string    `json:"screenshots,omitempty"`
	Profile        string    `json:"profile,omitempty"`
}

type viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// operation is one item of with.do. Exactly one operation field is set.
type operation struct {
	Goto       string       `json:"goto,omitempty"`
	Act        *actSpec     `json:"act,omitempty"`
	Extract    *extractSpec `json:"extract,omitempty"`
	Expect     *condition   `json:"expect,omitempty"`
	Wait       *waitSpec    `json:"wait,omitempty"`
	Screenshot string       `json:"screenshot,omitempty"`
	Ask        *askSpec     `json:"ask,omitempty"`
	When       *condition   `json:"when,omitempty"`
	Timeout    string       `json:"timeout,omitempty"`
}

type actSpec struct {
	Instruction string `json:"instruction"`
	Cache       *bool  `json:"cache,omitempty"`
}

type extractSpec struct {
	Instruction string         `json:"instruction"`
	Schema      map[string]any `json:"schema"`
}

type waitSpec struct {
	Selector string `json:"selector,omitempty"`
	Duration string `json:"duration,omitempty"`
}

type askSpec struct {
	Prompt  string `json:"prompt"`
	As      string `json:"as"`
	Timeout string `json:"timeout,omitempty"`
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
	case o.Goto != "":
		return opGoto
	case o.Act != nil:
		return opAct
	case o.Extract != nil:
		return opExtract
	case o.Expect != nil:
		return opExpect
	case o.Wait != nil:
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

func (c config) headless() bool {
	return c.Browser.Headless == nil || *c.Browser.Headless
}

func (c config) cacheEnabled() bool {
	return c.Cache == nil || *c.Cache
}

func (c config) screenshotPolicy() string {
	if c.Browser.Screenshots == "" {
		return screenshotsOnFailure
	}
	return c.Browser.Screenshots
}

// capturesFinalScreenshot reports whether a successful step saves a
// screenshot of the page it ends on.
func (c config) capturesFinalScreenshot() bool {
	policy := c.screenshotPolicy()
	return policy == screenshotsFinal || policy == screenshotsEach
}

func (c config) hasAsk() bool {
	for _, op := range c.Do {
		if op.Ask != nil {
			return true
		}
	}
	return false
}

func parseConfig(raw map[string]any) (config, error) {
	var cfg config
	if err := registry.ValidateExecutorConfig(executorType, raw); err != nil {
		return cfg, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("browser configuration: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("browser configuration: %w", err)
	}
	return cfg, nil
}

func validateStep(step ir.Step) error {
	if step.LLM == nil {
		return errors.New("browser actions need a model: set llm at the DAG level or with.llm on the step")
	}
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return err
	}
	return cfg.validate()
}

func (c config) validate() error {
	if len(c.Do) == 0 {
		return errors.New("browser: with.do must list at least one operation")
	}
	for name := range c.Variables {
		if !agentstep.IdentifierPattern.MatchString(name) {
			return fmt.Errorf("browser: variable name %q must match %s", name, agentstep.IdentifierPattern)
		}
	}
	for _, pattern := range c.Browser.AllowedDomains {
		// References resolve at run time, where the resolved value is checked.
		if strings.Contains(pattern, "$") {
			continue
		}
		if err := validateDomainPattern(pattern); err != nil {
			return fmt.Errorf("browser: %w", err)
		}
	}
	if profile := c.Browser.Profile; profile != "" && !agentstep.FileNamePattern.MatchString(profile) {
		return fmt.Errorf("browser: profile %q must match %s", profile, agentstep.FileNamePattern)
	}
	outputs := make(map[string]int)
	asks := make(map[string]int)
	for i, op := range c.Do {
		if err := op.validate(); err != nil {
			return fmt.Errorf("browser: do[%d]: %w", i, err)
		}
		if op.Act != nil {
			for _, name := range agentstep.VariableReferences(op.Act.Instruction) {
				_, isVariable := c.Variables[name]
				_, isEarlierAsk := asks[name]
				if !isVariable && !isEarlierAsk {
					return fmt.Errorf("browser: do[%d]: act references %%%s%%, which is not in with.variables or an earlier ask", i, name)
				}
			}
		}
		if op.Extract != nil {
			properties, _ := op.Extract.Schema["properties"].(map[string]any)
			for name := range properties {
				if first, exists := outputs[name]; exists {
					return fmt.Errorf("browser: do[%d]: output %q is already extracted by do[%d]", i, name, first)
				}
				outputs[name] = i
			}
		}
		if op.Ask != nil {
			if _, exists := c.Variables[op.Ask.As]; exists {
				return fmt.Errorf("browser: do[%d]: ask.as %q collides with a variable", i, op.Ask.As)
			}
			if first, exists := asks[op.Ask.As]; exists {
				return fmt.Errorf("browser: do[%d]: ask.as %q is already used by do[%d]", i, op.Ask.As, first)
			}
			asks[op.Ask.As] = i
		}
	}
	return nil
}

func (o operation) validate() error {
	if o.kind() == "" {
		return errors.New("operation must set one of goto, act, extract, expect, wait, screenshot, or ask")
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
	case o.Expect != nil:
		if err := o.Expect.validate(); err != nil {
			return fmt.Errorf("expect: %w", err)
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
	case o.Wait != nil:
		if (o.Wait.Selector == "") == (o.Wait.Duration == "") {
			return errors.New("wait must set exactly one of selector or duration")
		}
		if err := agentstep.ValidateDuration("wait.duration", o.Wait.Duration); err != nil {
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

// conditionSchema accepts a statement or an object with exactly one fixed
// check.
func conditionSchema() *jsonschema.Schema {
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		agentstep.NonEmptyString(),
		{
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Properties: map[string]*jsonschema.Schema{
				"text":     agentstep.NonEmptyString(),
				"selector": agentstep.NonEmptyString(),
				"url":      agentstep.NonEmptyString(),
				"within":   agentstep.StringSchema(),
			},
			OneOf: []*jsonschema.Schema{
				{Required: []string{"text"}},
				{Required: []string{"selector"}},
				{Required: []string{"url"}},
			},
		},
	}}
}

var operationSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: agentstep.NoExtraProperties(),
	Properties: map[string]*jsonschema.Schema{
		opGoto: agentstep.NonEmptyString(),
		opAct: {
			Types:                []string{"string", "object"},
			MinLength:            new(1),
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"instruction"},
			Properties: map[string]*jsonschema.Schema{
				"instruction": agentstep.NonEmptyString(),
				"cache":       {Type: "boolean"},
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
		opExpect: conditionSchema(),
		opWait: {
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Properties: map[string]*jsonschema.Schema{
				"selector": agentstep.NonEmptyString(),
				"duration": agentstep.NonEmptyString(),
			},
		},
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
		{Required: []string{opGoto}},
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
		"url": agentstep.NonEmptyString(),
		"browser": {
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Properties: map[string]*jsonschema.Schema{
				"headless":   {Type: "boolean"},
				"executable": agentstep.NonEmptyString(),
				"viewport": {
					Type:                 "object",
					AdditionalProperties: agentstep.NoExtraProperties(),
					Required:             []string{"width", "height"},
					Properties: map[string]*jsonschema.Schema{
						"width":  {Type: "integer", Minimum: new(1.0)},
						"height": {Type: "integer", Minimum: new(1.0)},
					},
				},
				"proxy":           agentstep.NonEmptyString(),
				"allowed_domains": {Type: "array", Items: agentstep.NonEmptyString()},
				"screenshots":     {Type: "string", Enum: []any{screenshotsOnFailure, screenshotsFinal, screenshotsEach, screenshotsNever}},
				"profile":         agentstep.NonEmptyString(),
			},
		},
		"variables": {Type: "object", AdditionalProperties: agentstep.StringSchema()},
		"cache":     {Type: "boolean"},
		"do":        {Type: "array", MinItems: new(1), Items: operationSchema},
	},
}
