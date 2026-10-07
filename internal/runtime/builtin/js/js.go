// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package js runs a sandboxed JavaScript function body as a workflow step.
//
// The script receives the configured input as its only parameter and the
// returned value becomes the step's stdout. The runtime exposes the ECMAScript
// builtins, console, URL, and URLSearchParams. It has no module loader, host
// I/O, timers, or event loop.
package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	formatText = "text"
	formatJSON = "json"

	defaultTimeout = 60 * time.Second

	// maxCallStackSize bounds recursion so runaway scripts raise a RangeError
	// instead of exhausting the Go stack.
	maxCallStackSize = 10_000

	// programName appears in JavaScript stack frames as programName:line:col.
	programName = "script"

	// The wrapper turns the script into a function body so return works. The
	// prefix has no line break, which keeps script line numbers intact in
	// stack traces.
	wrapperPrefix = "(function(input){"
	wrapperSuffix = "\n})"

	jsonIndent = 2
)

// interruptReason is the value handed to goja.Runtime.Interrupt so Run can
// report why the script stopped.
type interruptReason string

const (
	reasonTimeout interruptReason = "timeout"
	reasonCancel  interruptReason = "cancel"
	reasonStop    interruptReason = "stop"
)

type js struct {
	stdout  io.Writer
	stderr  io.Writer
	program *goja.Program
	input   scriptInput
	timeout time.Duration

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
		format = formatText
	}
	if format != formatText && format != formatJSON {
		return nil, fmt.Errorf("js: invalid format %q (want text or json)", cfg.Format)
	}

	timeout := defaultTimeout
	if cfg.Timeout != "" {
		timeout, err = time.ParseDuration(cfg.Timeout)
		if err != nil || timeout <= 0 {
			return nil, fmt.Errorf("js: invalid timeout %q", cfg.Timeout)
		}
	}

	input, err := loadInput(ctx, cfg, hasInput, format)
	if err != nil {
		return nil, err
	}

	program, err := goja.Compile(programName, wrapperPrefix+step.Script+wrapperSuffix, false)
	if err != nil {
		return nil, fmt.Errorf("js: compile error: %w", err)
	}

	return &js{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		program: program,
		input:   input,
		timeout: timeout,
	}, nil
}

func loadInput(ctx context.Context, cfg jsConfig, hasInput bool, format string) (scriptInput, error) {
	switch {
	case hasInput:
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
		if format == formatJSON {
			if !json.Valid(data) {
				return scriptInput{}, fmt.Errorf("js: input_file %q is not valid JSON", path)
			}
			return scriptInput{json: data}, nil
		}
		text := string(data)
		return scriptInput{text: &text}, nil
	default:
		return scriptInput{}, nil
	}
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
	timer := time.NewTimer(e.timeout)
	defer timer.Stop()
	go func() {
		select {
		case <-timer.C:
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
	return writeResult(e.stdout, vm, stringify, result)
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
// as they do in JavaScript.
func writeResult(w io.Writer, vm *goja.Runtime, stringify goja.Callable, value goja.Value) error {
	if goja.IsUndefined(value) {
		return nil
	}
	if text, ok := value.Export().(string); ok {
		_, err := fmt.Fprintln(w, text)
		return err
	}
	encoded, err := stringify(goja.Undefined(), value, goja.Null(), vm.ToValue(jsonIndent))
	if err != nil {
		return classifyException(err, nil)
	}
	if goja.IsUndefined(encoded) {
		return nil
	}
	_, err = fmt.Fprintln(w, encoded.String())
	return err
}

func (e *js) classify(ctx context.Context, err error) error {
	if interrupted, ok := errors.AsType[*goja.InterruptedError](err); ok {
		switch interrupted.Value() {
		case reasonTimeout:
			return fmt.Errorf("js: timeout after %s", e.timeout)
		case reasonCancel:
			return fmt.Errorf("js: cancelled: %w", ctx.Err())
		default:
			return errors.New("js: stopped")
		}
	}
	return classifyException(err, e.stderr)
}

// classifyException turns a goja error into a step error. The JavaScript
// stack trace goes to stderr when a writer is available.
func classifyException(err error, stderr io.Writer) error {
	if overflow, ok := errors.AsType[*goja.StackOverflowError](err); ok {
		writeStack(stderr, overflow.String())
		return errors.New("js: RangeError: Maximum call stack size exceeded")
	}
	exception, ok := errors.AsType[*goja.Exception](err)
	if !ok {
		return fmt.Errorf("js: %w", err)
	}
	thrown := exception.Value()
	if obj, ok := thrown.(*goja.Object); ok && obj.ClassName() == "Error" {
		writeStack(stderr, obj.Get("stack").String())
		return fmt.Errorf("js: %s: %s", obj.Get("name").String(), obj.Get("message").String())
	}
	writeStack(stderr, exception.String())
	return fmt.Errorf("js: %s", thrown.String())
}

func writeStack(w io.Writer, stack string) {
	if w == nil || stack == "" {
		return
	}
	_, _ = fmt.Fprintln(w, stack)
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
	executor.RegisterExecutor("js", newJS, nil, registry.ExecutorCapabilities{Script: true})
}
