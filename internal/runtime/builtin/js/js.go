// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package js runs a sandboxed JavaScript function body as a workflow step.
//
// The script receives the configured input as its only parameter and the
// returned value becomes the step's stdout. The runtime exposes the ECMAScript
// builtins, console, URL, and URLSearchParams. It has no module loader, host
// I/O, or timers; promises settle only through the microtask queue drained
// after the script returns.
package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/url"
)

var (
	_ executor.Executor = (*js)(nil)
	_ executor.Stopper  = (*js)(nil)
)

const (
	formatAuto = "auto"
	formatText = "text"
	formatJSON = "json"

	defaultTimeout = 60 * time.Second

	// maxCallStackSize bounds recursion so runaway scripts raise a RangeError
	// instead of exhausting the Go stack.
	maxCallStackSize = 10_000

	// programName appears in JavaScript stack frames as programName:line:col.
	programName = "script"

	// The wrapper turns the script into an async function body so return and
	// await work. It occupies its own line, and wrapperLineOffset is
	// subtracted from reported positions so lines and columns match the YAML.
	wrapperPrefix     = "(async function(input){\n"
	wrapperSuffix     = "\n})"
	wrapperLineOffset = 1

	jsonIndent = 2

	undefinedNotice = "js: script returned undefined, nothing written to stdout"
)

// interruptReason is the value handed to goja.Runtime.Interrupt so Run can
// report why the script stopped.
type interruptReason string

const (
	reasonTimeout interruptReason = "timeout"
	reasonCancel  interruptReason = "cancel"
	reasonStop    interruptReason = "stop"
)

var (
	stackPosition   = regexp.MustCompile(`\b` + programName + `:(\d+):(\d+)`)
	compilePosition = regexp.MustCompile(`\bLine (\d+):(\d+)`)
)

type js struct {
	stdout  io.Writer
	stderr  io.Writer
	program *goja.Program
	input   scriptInput
	timeout time.Duration // zero disables the sandbox timer

	mu     sync.Mutex
	vm     *goja.Runtime
	killed bool
}

// scriptInput carries the input value to the VM. JSON-encoded values are
// parsed inside the VM so the script sees native objects and arrays.
type scriptInput struct {
	json []byte
	text *string
}

func newJS(ctx context.Context, step ir.Step) (executor.Executor, error) {
	if strings.TrimSpace(step.Script) == "" {
		return nil, errors.New("js: script is required")
	}

	var cfg jsConfig
	hasInput, err := decodeConfig(step.ExecutorConfig.Config, &cfg)
	if err != nil {
		return nil, fmt.Errorf("js: invalid configuration: %w", err)
	}
	if hasInput && cfg.InputFile != "" {
		return nil, errors.New("js: input and input_file are mutually exclusive")
	}
	format := cfg.Format
	if format == "" {
		format = formatAuto
	}
	if format != formatAuto && format != formatText && format != formatJSON {
		return nil, fmt.Errorf("js: invalid format %q (want auto, text, or json)", cfg.Format)
	}

	timeout, err := resolveTimeout(cfg.Timeout, step.Timeout)
	if err != nil {
		return nil, err
	}

	input, err := loadInput(ctx, cfg, hasInput, format)
	if err != nil {
		return nil, err
	}

	program, err := compileScript(step.Script)
	if err != nil {
		return nil, err
	}

	return &js{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		program: program,
		input:   input,
		timeout: timeout,
	}, nil
}

// resolveTimeout picks the sandbox timer. An explicit with.timeout wins; a
// step timeout alone is left to the step context; otherwise the default
// applies so a runaway script cannot hang the run.
func resolveTimeout(configured any, stepTimeout time.Duration) (time.Duration, error) {
	if configured == nil {
		if stepTimeout > 0 {
			return 0, nil
		}
		return defaultTimeout, nil
	}
	if text, ok := configured.(string); ok {
		if seconds, err := strconv.ParseFloat(text, 64); err == nil {
			return secondsTimeout(seconds, configured)
		}
		timeout, err := time.ParseDuration(text)
		if err != nil || timeout <= 0 {
			return 0, fmt.Errorf("js: invalid timeout %q (want seconds or a duration such as 2m)", text)
		}
		return timeout, nil
	}
	value := reflect.ValueOf(configured)
	switch value.Kind() { //nolint:exhaustive // YAML and JSON decoders yield only these numeric kinds.
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return secondsTimeout(float64(value.Int()), configured)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return secondsTimeout(float64(value.Uint()), configured)
	case reflect.Float32, reflect.Float64:
		return secondsTimeout(value.Float(), configured)
	default:
		return 0, fmt.Errorf("js: invalid timeout %v (want seconds or a duration such as 2m)", configured)
	}
}

func secondsTimeout(seconds float64, configured any) (time.Duration, error) {
	if seconds <= 0 {
		return 0, fmt.Errorf("js: invalid timeout %v (want seconds or a duration such as 2m)", configured)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func compileScript(script string) (*goja.Program, error) {
	program, err := goja.Compile(programName, wrapperPrefix+script+wrapperSuffix, false)
	if err != nil {
		return nil, fmt.Errorf("js: compile error: %s", shiftPositions(compilePosition, err.Error()))
	}
	return program, nil
}

// validateStep compiles the script at load time so dagu validate and the
// editor report syntax errors before a run exists.
func validateStep(step ir.Step) error {
	if strings.TrimSpace(step.Script) == "" {
		return nil
	}
	if _, err := compileScript(step.Script); err != nil {
		return ir.NewValidationError("with.script", nil, err)
	}
	return nil
}

func loadInput(ctx context.Context, cfg jsConfig, hasInput bool, format string) (scriptInput, error) {
	switch {
	case hasInput:
		if text, ok := cfg.Input.(string); ok {
			return stringInput(text, format, "input")
		}
		if format == formatJSON {
			return scriptInput{}, errors.New("js: format json requires input to be a string")
		}
		encoded, err := json.Marshal(cfg.Input)
		if err != nil {
			return scriptInput{}, fmt.Errorf("js: input must be JSON-serializable: %w", err)
		}
		return scriptInput{json: encoded}, nil
	case cfg.InputFile != "":
		path, err := runtime.ResolveString(ctx, cfg.InputFile, cmnvalue.WorkflowField("js.input_file"))
		if err != nil {
			return scriptInput{}, fmt.Errorf("js: failed to evaluate input_file: %w", err)
		}
		data, err := fileutil.ReadFile(path)
		if err != nil {
			return scriptInput{}, fmt.Errorf("js: reading input_file %q: %w", path, err)
		}
		return stringInput(string(data), format, fmt.Sprintf("input_file %q", path))
	default:
		return scriptInput{}, nil
	}
}

// stringInput applies the format to string input. Auto parses only JSON
// objects and arrays, so plain text and scalar-looking strings stay strings.
func stringInput(text, format, source string) (scriptInput, error) {
	switch format {
	case formatJSON:
		if !json.Valid([]byte(text)) {
			return scriptInput{}, fmt.Errorf("js: %s is not valid JSON", source)
		}
		return scriptInput{json: []byte(text)}, nil
	case formatAuto:
		if looksLikeJSONContainer(text) && json.Valid([]byte(text)) {
			return scriptInput{json: []byte(text)}, nil
		}
	}
	return scriptInput{text: &text}, nil
}

func looksLikeJSONContainer(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func (e *js) SetStdout(w io.Writer) { e.stdout = w }
func (e *js) SetStderr(w io.Writer) { e.stderr = w }

func (e *js) Kill(_ os.Signal) error {
	e.interrupt(reasonStop)
	return nil
}

func (e *js) Stop(_ cmdutil.TerminationIntent) error {
	e.interrupt(reasonStop)
	return nil
}

func (e *js) interrupt(reason interruptReason) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.killed = true
	if e.vm != nil {
		e.vm.Interrupt(reason)
	}
}

func (e *js) Run(ctx context.Context) error {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxCallStackSize)

	jsonObject := vm.Get("JSON").ToObject(vm)
	stringify, _ := goja.AssertFunction(jsonObject.Get("stringify"))
	parse, _ := goja.AssertFunction(jsonObject.Get("parse"))

	installConsole(vm, stringify, e.stderr)
	installURL(vm)

	e.mu.Lock()
	e.vm = vm
	killed := e.killed
	e.mu.Unlock()
	if killed {
		return errors.New("js: stopped")
	}

	done := make(chan struct{})
	defer close(done)
	var timer <-chan time.Time
	if e.timeout > 0 {
		t := time.NewTimer(e.timeout)
		defer t.Stop()
		timer = t.C
	}
	go func() {
		select {
		case <-timer:
			vm.Interrupt(reasonTimeout)
		case <-ctx.Done():
			vm.Interrupt(reasonCancel)
		case <-done:
		}
	}()

	fnValue, err := vm.RunProgram(e.program)
	if err != nil {
		return e.classify(ctx, err)
	}
	fn, ok := goja.AssertFunction(fnValue)
	if !ok {
		return errors.New("js: script did not compile to a function")
	}

	input, err := e.inputValue(vm, parse)
	if err != nil {
		return e.classify(ctx, err)
	}

	result, err := fn(goja.Undefined(), input)
	if err != nil {
		return e.classify(ctx, err)
	}
	result, err = e.settle(result)
	if err != nil {
		return err
	}
	return e.writeResult(vm, stringify, result)
}

// settle unwraps the promise returned by the async wrapper. The microtask
// queue has already drained, so a promise that is still pending can never
// resolve.
func (e *js) settle(value goja.Value) (goja.Value, error) {
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return value, nil
	}
	switch promise.State() {
	case goja.PromiseStateFulfilled:
		return e.settle(promise.Result())
	case goja.PromiseStateRejected:
		return nil, e.rejection(promise.Result())
	default:
		return nil, errors.New("js: script returned a promise that never settled; the sandbox has no event loop, so only promises that resolve synchronously are supported")
	}
}

func (e *js) inputValue(vm *goja.Runtime, parse goja.Callable) (goja.Value, error) {
	switch {
	case e.input.json != nil:
		return parse(goja.Undefined(), vm.ToValue(string(e.input.json)))
	case e.input.text != nil:
		return vm.ToValue(*e.input.text), nil
	default:
		return goja.Undefined(), nil
	}
}

// writeResult publishes the returned value. Strings are written as-is so
// downstream output capture sees plain text; other values are serialized by
// the VM's own JSON.stringify so Date, toJSON, and circular references behave
// as they do in JavaScript. An undefined result leaves stdout empty and says
// so on stderr, since a missing return is the usual cause.
func (e *js) writeResult(vm *goja.Runtime, stringify goja.Callable, value goja.Value) error {
	if goja.IsUndefined(value) {
		_, _ = fmt.Fprintln(e.stderr, undefinedNotice)
		return nil
	}
	if text, ok := value.Export().(string); ok {
		_, err := fmt.Fprintln(e.stdout, text)
		return err
	}
	encoded, err := stringify(goja.Undefined(), value, goja.Null(), vm.ToValue(jsonIndent))
	if err != nil {
		return e.classify(context.Background(), err)
	}
	if goja.IsUndefined(encoded) {
		_, _ = fmt.Fprintln(e.stderr, undefinedNotice)
		return nil
	}
	_, err = fmt.Fprintln(e.stdout, encoded.String())
	return err
}

func (e *js) classify(ctx context.Context, err error) error {
	if interrupted, ok := errors.AsType[*goja.InterruptedError](err); ok {
		switch interrupted.Value() {
		case reasonTimeout:
			return fmt.Errorf("js: timeout after %s; raise with.timeout to allow longer scripts", formatDuration(e.timeout))
		case reasonCancel:
			return fmt.Errorf("js: cancelled: %w", ctx.Err())
		default:
			return errors.New("js: stopped")
		}
	}
	if overflow, ok := errors.AsType[*goja.StackOverflowError](err); ok {
		e.writeStack(overflow.String())
		return errors.New("js: RangeError: Maximum call stack size exceeded")
	}
	exception, ok := errors.AsType[*goja.Exception](err)
	if !ok {
		return fmt.Errorf("js: %w", err)
	}
	return e.rejection(exception.Value())
}

// rejection turns a thrown or rejected value into the step error. Error
// objects carry their own stack, which names the script line; the adjusted
// trace goes to stderr and the line goes into the error itself.
func (e *js) rejection(thrown goja.Value) error {
	if obj, ok := thrown.(*goja.Object); ok && obj.ClassName() == "Error" {
		stack := shiftPositions(stackPosition, obj.Get("stack").String())
		e.writeStack(stack)
		return fmt.Errorf("js: %s: %s%s", obj.Get("name").String(), obj.Get("message").String(), scriptLocation(stack))
	}
	return fmt.Errorf("js: %s", thrown.String())
}

func (e *js) writeStack(stack string) {
	if e.stderr == nil || stack == "" {
		return
	}
	_, _ = fmt.Fprintln(e.stderr, stack)
}

// scriptLocation names the innermost script line in an adjusted stack trace.
func scriptLocation(stack string) string {
	match := stackPosition.FindStringSubmatch(stack)
	if match == nil {
		return ""
	}
	return fmt.Sprintf(" (script line %s)", match[1])
}

// shiftPositions rewrites line:column pairs so they count from the first
// script line rather than the wrapper line.
func shiftPositions(pattern *regexp.Regexp, text string) string {
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := pattern.FindStringSubmatch(match)
		line, _ := strconv.Atoi(parts[1])
		if line > wrapperLineOffset {
			line -= wrapperLineOffset
		}
		return strings.Replace(match, parts[1]+":"+parts[2], strconv.Itoa(line)+":"+parts[2], 1)
	})
}

func formatDuration(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return d.String()
}

// installConsole binds a console object whose methods write to the step's
// stderr, keeping stdout reserved for the returned value.
func installConsole(vm *goja.Runtime, stringify goja.Callable, stderr io.Writer) {
	console := vm.NewObject()
	write := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			parts = append(parts, formatConsoleArg(arg, stringify))
		}
		_, _ = fmt.Fprintln(stderr, strings.Join(parts, " "))
		return goja.Undefined()
	}
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		_ = console.Set(level, write)
	}
	_ = vm.Set("console", console)
}

func formatConsoleArg(arg goja.Value, stringify goja.Callable) string {
	if _, ok := arg.Export().(string); ok {
		return arg.String()
	}
	encoded, err := stringify(goja.Undefined(), arg)
	if err != nil || goja.IsUndefined(encoded) {
		return arg.String()
	}
	return encoded.String()
}

// installURL exposes the WHATWG URL classes without a module loader, so no
// require function reaches the script.
func installURL(vm *goja.Runtime) {
	module := vm.NewObject()
	exports := vm.NewObject()
	_ = module.Set("exports", exports)
	url.Require(vm, module)
	_ = vm.Set("URL", exports.Get("URL"))
	_ = vm.Set("URLSearchParams", exports.Get("URLSearchParams"))
}

func init() {
	executor.RegisterExecutor("js", newJS, validateStep, registry.ExecutorCapabilities{Script: true})
}
