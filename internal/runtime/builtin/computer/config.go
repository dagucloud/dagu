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
	// defaultIdle is short enough that a person who pauses to watch the step
	// does not hold it long, and long enough to finish a word or a click.
	defaultIdle       = 5 * time.Second
	defaultFindWithin = 10 * time.Second
	// defaultMaxRows bounds a table read when nothing sets it.
	defaultMaxRows = 10000
)

// How a replayed action reaches the application: through the pointer and
// keys, as the model's did, or through the element's own automation.
const (
	inputScreen = "screen"
	inputDirect = "direct"
)

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{LLM: true})
}

// config is the resolved with block of a computer step.
type config struct {
	Mode      string            `json:"mode,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
	// Visible names variables whose values the model may see, for values
	// that are not secret, such as the item a row chooses from a list.
	Visible        []string `json:"visible,omitempty"`
	Screenshots    string   `json:"screenshots,omitempty"`
	AI             string   `json:"ai,omitempty"`
	Cache          *bool    `json:"cache,omitempty"`
	MaxActions     int      `json:"max_actions,omitempty"`
	OnConfirmation string   `json:"on_confirmation,omitempty"`
	Idle           string   `json:"idle,omitempty"`
	// FindWithin is how long a replayed action waits for its element, or a
	// replayed turn for its screen, before it is a miss.
	FindWithin string `json:"find_within,omitempty"`
	// Input is how a replayed action reaches the application: screen, the
	// default, or direct.
	Input string `json:"input,omitempty"`
	// MaxRows bounds how many rows an extract reads from a table.
	MaxRows int `json:"max_rows,omitempty"`
	// Maximize, true unless set false, maximizes the front window before an
	// operation looks at the screen.
	Maximize *bool `json:"maximize,omitempty"`
	// Handle lists screens that may come up at any point of the step, each
	// with the pinned steps that clear it.
	Handle []handler   `json:"handle,omitempty"`
	Do     []operation `json:"do"`
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

// maximize is the operation's own maximize setting, if it has one.
func (o operation) maximize() *bool {
	switch {
	case o.Act != nil:
		return o.Act.Maximize
	case o.Extract != nil:
		return o.Extract.Maximize
	}
	return nil
}

// looksAtScreen reports an operation that reads or acts on the screen, or
// checks it before running, which the front window is maximized for.
func (o operation) looksAtScreen() bool {
	return o.Act != nil || o.Extract != nil || (o.Expect != nil && !o.Expect.onValue()) || o.Screenshot != "" || (o.When != nil && !o.When.onValue())
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
	FindWithin  string `json:"find_within,omitempty"`
	Input       string `json:"input,omitempty"`
	Maximize    *bool  `json:"maximize,omitempty"`
	// Recorded holds the act's pinned steps, keyed by system (windows or
	// macos). A device of that system runs them first, ignoring its own
	// recording, so a learned act travels across devices.
	Recorded map[string][]pinnedStep `json:"recorded,omitempty"`
}

type extractSpec struct {
	Instruction string         `json:"instruction"`
	AI          string         `json:"ai,omitempty"`
	MaxRows     int            `json:"max_rows,omitempty"`
	Maximize    *bool          `json:"maximize,omitempty"`
	Schema      map[string]any `json:"schema"`
	// Fields holds the extract's pinned bindings, keyed by system then by
	// output name, so a device reads them with no model even if it never
	// learned them.
	Fields map[string]map[string]pinnedField `json:"fields,omitempty"`
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

// maxRows returns how many rows an extract reads from a table: the
// extract's setting, the step's, or the default.
func (c config) maxRows(spec extractSpec) int {
	switch {
	case spec.MaxRows > 0:
		return spec.MaxRows
	case c.MaxRows > 0:
		return c.MaxRows
	default:
		return defaultMaxRows
	}
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

// findWithin returns how long a replay of an act waits for an element or
// a screen: the act's setting, the step's, or fallback. A setting that
// still cannot be read when the act runs, such as a reference that did not
// resolve, is an error.
func (c config) findWithin(spec actSpec, fallback time.Duration) (time.Duration, error) {
	for _, value := range []string{spec.FindWithin, c.FindWithin} {
		if value == "" {
			continue
		}
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 {
			return 0, fmt.Errorf("find_within %q must be a duration such as 10s, or 0 to look once", value)
		}
		return d, nil
	}
	return fallback, nil
}

// direct reports whether a replay of an act drives elements through their
// own patterns: the act's input setting, the step's, or the screen. The
// schema admits only the two values; anything else is an error.
func (c config) direct(spec actSpec) (bool, error) {
	for _, value := range []string{spec.Input, c.Input} {
		switch value {
		case "":
			continue
		case inputScreen:
			return false, nil
		case inputDirect:
			return true, nil
		default:
			return false, fmt.Errorf("input %q must be screen or direct", value)
		}
	}
	return false, nil
}

// maximizes reports whether the front window is maximized before an
// operation looks at the screen: the operation's own setting, the step's,
// or true.
func (c config) maximizes(own *bool) bool {
	for _, value := range []*bool{own, c.Maximize} {
		if value != nil {
			return *value
		}
	}
	return true
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
			return fmt.Errorf("computer: idle %q must be a duration such as 5s, or 0 to not wait", c.Idle)
		}
	}
	if err := validateFindWithin(c.FindWithin); err != nil {
		return fmt.Errorf("computer: %w", err)
	}
	for name := range c.Variables {
		if !agentstep.IdentifierPattern.MatchString(name) {
			return fmt.Errorf("computer: variable name %q must match %s", name, agentstep.IdentifierPattern)
		}
	}
	for _, name := range c.Visible {
		if _, ok := c.Variables[name]; !ok {
			return fmt.Errorf("computer: visible names %q, which is not in with.variables", name)
		}
	}
	for i, h := range c.Handle {
		if err := h.validate(); err != nil {
			return fmt.Errorf("computer: handle[%d]: %w", i, err)
		}
		for _, name := range h.references() {
			if _, ok := c.Variables[name]; !ok {
				return fmt.Errorf("computer: handle[%d] references %%%s%%, which is not in with.variables", i, name)
			}
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
			names := agentstep.VariableReferences(op.Act.Instruction)
			for _, steps := range op.Act.Recorded {
				for _, step := range steps {
					names = append(names, step.references()...)
				}
			}
			references[opAct] = names
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
		if len(bindableFields(o.Extract.Schema)) == 0 {
			return errors.New("extract under never has no field that reads from an element; every field needs the model")
		}
		if unbindable := unbindableFields(o.Extract.Schema); len(unbindable) > 0 {
			return fmt.Errorf("extract under never cannot read %s without the model; set ai: on_miss or every_run", strings.Join(unbindable, ", "))
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
		if err := validateFindWithin(o.Act.FindWithin); err != nil {
			return fmt.Errorf("act: %w", err)
		}
		if err := validateRecorded(o.Act.Recorded); err != nil {
			return fmt.Errorf("act: %w", err)
		}
	case o.Extract != nil:
		if strings.TrimSpace(o.Extract.Instruction) == "" {
			return errors.New("extract instruction must not be empty")
		}
		if o.Extract.Schema["type"] != "object" {
			return errors.New(`extract schema must have type: object`)
		}
		if err := validateFields(o.Extract.Fields, o.Extract.Schema); err != nil {
			return fmt.Errorf("extract: %w", err)
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

// validateFindWithin checks a find_within value, which may be 0 to look
// once. A value holding a reference resolves at run time.
func validateFindWithin(value string) error {
	if value == "" || strings.Contains(value, "$") {
		return nil
	}
	if d, err := time.ParseDuration(value); err != nil || d < 0 {
		return fmt.Errorf("find_within %q must be a duration such as 10s, or 0 to look once", value)
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

// inputSchema accepts how a replayed action reaches the application.
func inputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: []any{inputScreen, inputDirect}}
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
				"statement":  agentstep.NonEmptyString(),
				"text":       agentstep.NonEmptyString(),
				"element":    {Ref: "#/$defs/selector"},
				"window":     agentstep.NonEmptyString(),
				"value":      agentstep.StringSchema(),
				"equals":     agentstep.StringSchema(),
				"not_equals": agentstep.StringSchema(),
				"within":     agentstep.StringSchema(),
			},
			OneOf: []*jsonschema.Schema{
				{Required: []string{"statement"}},
				{Required: []string{"text"}},
				{Required: []string{"element"}},
				{Required: []string{"window"}},
				{Required: []string{"value"}},
			},
		},
	}}
}

// selectorSchema accepts a selector as a condition names an element. A
// container is a selector too, through the definition the step's schema
// holds.
// pinnedBySystem wraps a per-system value in an object keyed by the systems
// a pin may name. The value is built fresh for each system, so the schema
// stays a tree.
func pinnedBySystem(value func() *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Properties:           map[string]*jsonschema.Schema{systemWindows: value(), systemMacOS: value()},
	}
}

// handlerSchema accepts a handler: an exact check and the pinned steps that
// clear the screen it finds.
func handlerSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Required:             []string{"when", "recorded"},
		Properties: map[string]*jsonschema.Schema{
			"name":     agentstep.NonEmptyString(),
			"when":     exactCheckSchema(),
			"recorded": pinnedBySystem(pinnedStepsSchema),
		},
	}
}

// exactCheckSchema accepts a check read from the window's elements alone:
// text, an element, or a window title, without within.
func exactCheckSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Properties: map[string]*jsonschema.Schema{
			"text":    agentstep.NonEmptyString(),
			"element": {Ref: "#/$defs/selector"},
			"window":  agentstep.NonEmptyString(),
		},
		OneOf: []*jsonschema.Schema{{Required: []string{"text"}}, {Required: []string{"element"}}, {Required: []string{"window"}}},
	}
}

// pinnedStepsSchema accepts a non-empty list of pinned steps.
func pinnedStepsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "array", MinItems: new(1), Items: pinnedStepSchema()}
}

// checkboxSelector accepts a selector for a checkbox alone, as check and
// uncheck take.
func checkboxSelector() *jsonschema.Schema {
	return &jsonschema.Schema{AllOf: []*jsonschema.Schema{
		{Ref: "#/$defs/selector"},
		{Required: []string{"role"}, Properties: map[string]*jsonschema.Schema{"role": {Enum: []any{desktop.RoleCheckbox}}}},
	}}
}

// pinnedStepSchema accepts exactly one action of a pinned step.
func pinnedStepSchema() *jsonschema.Schema {
	selector := func() *jsonschema.Schema { return &jsonschema.Schema{Ref: "#/$defs/selector"} }
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Properties: map[string]*jsonschema.Schema{
			"click":        selector(),
			"double_click": selector(),
			"right_click":  selector(),
			"check":        checkboxSelector(),
			"uncheck":      checkboxSelector(),
			"type": {
				Type:                 "object",
				AdditionalProperties: agentstep.NoExtraProperties(),
				Required:             []string{"into", "text"},
				Properties:           map[string]*jsonschema.Schema{"into": selector(), "text": agentstep.StringSchema()},
			},
			"key": agentstep.NonEmptyString(),
			"scroll": {
				Type:                 "object",
				AdditionalProperties: agentstep.NoExtraProperties(),
				Required:             []string{"on", "dy"},
				Properties:           map[string]*jsonschema.Schema{"on": selector(), "dy": {Type: "integer"}},
			},
			"wait":  agentstep.NonEmptyString(),
			"focus": selector(),
		},
		OneOf: []*jsonschema.Schema{
			{Required: []string{"click"}}, {Required: []string{"double_click"}}, {Required: []string{"right_click"}},
			{Required: []string{"check"}}, {Required: []string{"uncheck"}},
			{Required: []string{"type"}}, {Required: []string{"key"}}, {Required: []string{"scroll"}}, {Required: []string{"wait"}},
		},
	}
}

// pinnedFieldSchema accepts an extract's pinned field: a selector for a
// scalar, or a container and columns alone for a table, whose selector
// keys go inside container.
func pinnedFieldSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		If:                   &jsonschema.Schema{Required: []string{"container"}},
		Then:                 &jsonschema.Schema{PropertyNames: &jsonschema.Schema{Enum: []any{"container", "columns"}}},
		DependentRequired:    map[string][]string{"columns": {"container"}},
		Properties: map[string]*jsonschema.Schema{
			"role":      {Type: "string", Enum: roleValues()},
			"name":      agentstep.NonEmptyString(),
			"id":        agentstep.NonEmptyString(),
			"app":       agentstep.NonEmptyString(),
			"window":    agentstep.NonEmptyString(),
			"in":        {Ref: "#/$defs/selector"},
			"near":      nearSchema(),
			"nth":       {Type: "integer", Minimum: new(0.0)},
			"enabled":   {Type: "boolean"},
			"container": {Ref: "#/$defs/selector"},
			"columns":   {Type: "object", AdditionalProperties: agentstep.NonEmptyString()},
		},
	}
}

// roleValues is the role enum as schema values.
func roleValues() []any {
	roles := make([]any, 0, len(desktop.Roles))
	for _, role := range desktop.Roles {
		roles = append(roles, role)
	}
	return roles
}

// nearSchema is the near clause of a selector.
func nearSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Required:             []string{"label", "side"},
		Properties: map[string]*jsonschema.Schema{
			"label": agentstep.NonEmptyString(),
			"side":  {Type: "string", Enum: []any{desktop.SideRight, desktop.SideBelow, desktop.SideLeft, desktop.SideAbove}},
		},
	}
}

func selectorSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: agentstep.NoExtraProperties(),
		Required:             []string{"role"},
		Properties: map[string]*jsonschema.Schema{
			"role":    {Type: "string", Enum: roleValues()},
			"name":    agentstep.NonEmptyString(),
			"id":      agentstep.NonEmptyString(),
			"app":     agentstep.NonEmptyString(),
			"window":  agentstep.NonEmptyString(),
			"in":      {Ref: "#/$defs/selector"},
			"near":    nearSchema(),
			"nth":     {Type: "integer", Minimum: new(0.0)},
			"enabled": {Type: "boolean"},
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
				"find_within": agentstep.StringSchema(),
				"input":       inputSchema(),
				"maximize":    {Type: "boolean"},
				"recorded":    pinnedBySystem(pinnedStepsSchema),
			},
		},
		opExtract: {
			Type:                 "object",
			AdditionalProperties: agentstep.NoExtraProperties(),
			Required:             []string{"instruction", "schema"},
			Properties: map[string]*jsonschema.Schema{
				"instruction": agentstep.NonEmptyString(),
				"ai":          aiSchema(),
				"max_rows":    positiveInteger(),
				"maximize":    {Type: "boolean"},
				"schema":      {Type: "object"},
				"fields": pinnedBySystem(func() *jsonschema.Schema {
					return &jsonschema.Schema{Type: "object", AdditionalProperties: pinnedFieldSchema()}
				}),
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
		"visible":         {Type: "array", Items: agentstep.StringSchema()},
		"screenshots":     {Type: "string", Enum: []any{screenshotsOnFailure, screenshotsFinal, screenshotsEach, screenshotsNever}},
		"ai":              aiSchema(),
		"cache":           {Type: "boolean"},
		"max_actions":     positiveInteger(),
		"on_confirmation": {Type: "string", Enum: []any{confirmationFail, confirmationAllow}},
		"idle":            agentstep.StringSchema(),
		"find_within":     agentstep.StringSchema(),
		"input":           inputSchema(),
		"max_rows":        positiveInteger(),
		"maximize":        {Type: "boolean"},
		"handle":          {Type: "array", Items: handlerSchema()},
		"do":              {Type: "array", MinItems: new(1), Items: operationSchema},
	},
	Defs: map[string]*jsonschema.Schema{"selector": selectorSchema()},
}
