// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package js

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runResult struct {
	stdout string
	stderr string
	err    error
}

func newStep(script string, cfg map[string]any) ir.Step {
	return ir.Step{
		Script:         script,
		ExecutorConfig: ir.ExecutorConfig{Type: "js", Config: cfg},
	}
}

func run(t *testing.T, ctx context.Context, script string, cfg map[string]any) runResult {
	t.Helper()
	exec, err := newJS(ctx, newStep(script, cfg))
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)
	runErr := exec.Run(ctx)
	return runResult{stdout: stdout.String(), stderr: stderr.String(), err: runErr}
}

func TestJS_Output(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		cfg    map[string]any
		want   string
	}{
		{name: "Object", script: `return {a: 1, b: "x"}`, want: "{\n  \"a\": 1,\n  \"b\": \"x\"\n}\n"},
		{name: "Array", script: `return [1, 2]`, want: "[\n  1,\n  2\n]\n"},
		{name: "StringRaw", script: `return "hi"`, want: "hi\n"},
		{name: "Number", script: `return 42`, want: "42\n"},
		{name: "Null", script: `return null`, want: "null\n"},
		{name: "NoReturn", script: `const x = 1;`, want: ""},
		{name: "Undefined", script: `return undefined`, want: ""},
		{name: "Function", script: `return function() {}`, want: ""},
		{name: "DroppedProps", script: `return {f: function(){}, u: undefined, k: 1}`, want: "{\n  \"k\": 1\n}\n"},
		{name: "Date", script: `return new Date(0)`, want: "\"1970-01-01T00:00:00.000Z\"\n"},
		{name: "ToJSON", script: `return {toJSON() { return "custom" }}`, want: "\"custom\"\n"},
		{name: "KeyOrder", script: `return {z: 1, a: 2}`, want: "{\n  \"z\": 1,\n  \"a\": 2\n}\n"},
		{name: "TemplateLiteral", script: "return `v=${input.x}`", cfg: map[string]any{"input": map[string]any{"x": 1}}, want: "v=1\n"},
		{name: "ResolvedPromise", script: `return Promise.resolve({ok: true})`, want: "{\n  \"ok\": true\n}\n"},
		{name: "Await", script: `const v = await Promise.resolve(2); return v * 21`, want: "42\n"},
		{name: "AwaitAll", script: `const [a, b] = await Promise.all([Promise.resolve(1), 2]); return a + b`, want: "3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := run(t, context.Background(), tt.script, tt.cfg)
			require.NoError(t, got.err)
			assert.Equal(t, tt.want, got.stdout)
		})
	}
}

func TestJS_Input(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		input  any
		want   string
	}{
		{name: "Object", script: `return input.a + input.list.length`, input: map[string]any{"a": 1, "list": []any{"x", "y"}}, want: "3\n"},
		{name: "NestedNative", script: `input.n.x = {y: 2}; return input.n.x.y + (Array.isArray(input.list) ? 1 : 0)`, input: map[string]any{"n": map[string]any{}, "list": []any{}}, want: "3\n"},
		{name: "String", script: `return input.toUpperCase()`, input: "abc", want: "ABC\n"},
		{name: "Number", script: `return input * 2`, input: 21, want: "42\n"},
		{name: "Bool", script: `return typeof input`, input: true, want: "boolean\n"},
		{name: "Null", script: `return input === null`, input: nil, want: "true\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := run(t, context.Background(), tt.script, map[string]any{"input": tt.input})
			require.NoError(t, got.err)
			assert.Equal(t, tt.want, got.stdout)
		})
	}

	t.Run("Unset", func(t *testing.T) {
		t.Parallel()
		got := run(t, context.Background(), `return typeof input`, nil)
		require.NoError(t, got.err)
		assert.Equal(t, "undefined\n", got.stdout)
	})

	t.Run("StringParsedAsJSON", func(t *testing.T) {
		t.Parallel()
		got := run(t, context.Background(), `return input.items.length`, map[string]any{"input": `{"items":[1,2]}`, "format": "json"})
		require.NoError(t, got.err)
		assert.Equal(t, "2\n", got.stdout)
	})

	t.Run("AutoFormat", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			name  string
			input string
			want  string
		}{
			{name: "Object", input: ` {"a":1}`, want: "object:1\n"},
			{name: "Array", input: `[1,2,3]`, want: "object:3\n"},
			{name: "Scalar", input: `123`, want: "string:3\n"},
			{name: "HTML", input: `<a href="/x">`, want: "string:13\n"},
			{name: "InvalidJSON", input: `{not json`, want: "string:9\n"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := run(t, context.Background(), `return typeof input + ":" + (input.length ?? input.a)`, map[string]any{"input": tt.input})
				require.NoError(t, got.err)
				assert.Equal(t, tt.want, got.stdout)
			})
		}
	})

	t.Run("TextFormatKeepsJSONString", func(t *testing.T) {
		t.Parallel()
		got := run(t, context.Background(), `return typeof input`, map[string]any{"input": `{"a":1}`, "format": "text"})
		require.NoError(t, got.err)
		assert.Equal(t, "string\n", got.stdout)
	})
}

func TestResolveTimeout(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		configured any
		step       time.Duration
		want       time.Duration
		wantErr    string
	}{
		{name: "Default", want: defaultTimeout},
		{name: "DefersToStep", step: 5 * time.Minute, want: 0},
		{name: "Seconds", configured: 30, want: 30 * time.Second},
		{name: "SecondsUint64", configured: uint64(30), want: 30 * time.Second},
		{name: "SecondsFloat", configured: 1.5, want: 1500 * time.Millisecond},
		{name: "SecondsString", configured: "30", want: 30 * time.Second},
		{name: "Duration", configured: "2m", want: 2 * time.Minute},
		{name: "ExplicitBeatsStep", configured: "1s", step: time.Hour, want: time.Second},
		{name: "Zero", configured: 0, wantErr: "invalid timeout"},
		{name: "Negative", configured: "-5s", wantErr: "invalid timeout"},
		{name: "Garbage", configured: "soon", wantErr: "invalid timeout"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveTimeout(tt.configured, tt.step)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJS_InputFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "data.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"city":"Metropolis"}`), 0o600))
	textPath := filepath.Join(dir, "page.html")
	require.NoError(t, os.WriteFile(textPath, []byte(`<a href="/a">x</a>`), 0o600))

	t.Run("TextDefault", func(t *testing.T) {
		t.Parallel()
		got := run(t, context.Background(), `return input.match(/href="([^"]+)"/)[1]`, map[string]any{"input_file": textPath})
		require.NoError(t, got.err)
		assert.Equal(t, "/a\n", got.stdout)
	})

	t.Run("JSON", func(t *testing.T) {
		t.Parallel()
		got := run(t, context.Background(), `return input.city`, map[string]any{"input_file": jsonPath, "format": "json"})
		require.NoError(t, got.err)
		assert.Equal(t, "Metropolis\n", got.stdout)
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		_, err := newJS(context.Background(), newStep(`return 1`, map[string]any{"input_file": textPath, "format": "json"}))
		require.ErrorContains(t, err, "not valid JSON")
	})

	t.Run("Missing", func(t *testing.T) {
		t.Parallel()
		_, err := newJS(context.Background(), newStep(`return 1`, map[string]any{"input_file": filepath.Join(dir, "nope")}))
		require.ErrorContains(t, err, "reading input_file")
	})
}

func TestJS_ConfigErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		cfg    map[string]any
		want   string
	}{
		{name: "EmptyScript", script: "  ", want: "script is required"},
		{name: "BothInputs", script: "return 1", cfg: map[string]any{"input": 1, "input_file": "x"}, want: "mutually exclusive"},
		{name: "BadFormat", script: "return 1", cfg: map[string]any{"format": "xml"}, want: `invalid format "xml"`},
		{name: "BadFormatMentionsAuto", script: "return 1", cfg: map[string]any{"format": "xml"}, want: "want auto, text, or json"},
		{name: "JSONFormatNonString", script: "return 1", cfg: map[string]any{"input": 1, "format": "json"}, want: "requires input to be a string"},
		{name: "JSONFormatInvalid", script: "return 1", cfg: map[string]any{"input": "{", "format": "json"}, want: "input is not valid JSON"},
		{name: "BadTimeout", script: "return 1", cfg: map[string]any{"timeout": "soon"}, want: `invalid timeout "soon"`},
		{name: "ZeroTimeout", script: "return 1", cfg: map[string]any{"timeout": "0s"}, want: `invalid timeout "0s"`},
		{name: "UnknownField", script: "return 1", cfg: map[string]any{"args": 1}, want: "invalid configuration"},
		{name: "SyntaxError", script: "return {", want: "compile error: SyntaxError"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newJS(context.Background(), newStep(tt.script, tt.cfg))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestJS_UndefinedNotice(t *testing.T) {
	t.Parallel()

	got := run(t, context.Background(), `const x = 1;`, nil)
	require.NoError(t, got.err)
	assert.Empty(t, got.stdout)
	assert.Equal(t, "js: script returned undefined, nothing written to stdout\n", got.stderr)

	got = run(t, context.Background(), `return "ok"`, nil)
	require.NoError(t, got.err)
	assert.Empty(t, got.stderr)
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateStep(newStep("return 1", nil)))
	require.NoError(t, validateStep(newStep("", nil)))
	err := validateStep(newStep("const a = 1;\nconst x = ;", nil))
	require.ErrorContains(t, err, "js: compile error: SyntaxError")
	require.ErrorContains(t, err, "Line 2:11")
	err = validateStep(newStep("return 1 +;", nil))
	require.ErrorContains(t, err, "Line 1:11")
}

func TestJS_Console(t *testing.T) {
	t.Parallel()

	got := run(t, context.Background(), `
console.log("a", {b: 1}, 2);
console.error("err");
console.warn(undefined, null);
return "done"`, nil)
	require.NoError(t, got.err)
	assert.Equal(t, "done\n", got.stdout)
	assert.Equal(t, "a {\"b\":1} 2\nerr\nundefined null\n", got.stderr)
}

func TestJS_URL(t *testing.T) {
	t.Parallel()

	got := run(t, context.Background(), `
const u = new URL("/p?q=1", "https://example.com/base/");
const sp = new URLSearchParams("a=1&b=2");
return [u.href, u.searchParams.get("q"), sp.get("b")]`, nil)
	require.NoError(t, got.err)
	assert.JSONEq(t, `["https://example.com/p?q=1","1","2"]`, got.stdout)
}

func TestJS_Sandbox(t *testing.T) {
	t.Parallel()

	got := run(t, context.Background(), `
return [typeof require, typeof fetch, typeof process, typeof setTimeout, typeof module, typeof exports, typeof globalThis.require]`, nil)
	require.NoError(t, got.err)
	assert.JSONEq(t, `["undefined","undefined","undefined","undefined","undefined","undefined","undefined"]`, got.stdout)
}

func TestJS_RuntimeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		script     string
		wantErr    string
		wantStderr string
	}{
		{name: "Error", script: `throw new Error("boom")`, wantErr: "js: Error: boom (script line 1)", wantStderr: "script:1"},
		{name: "TypeError", script: `null.x`, wantErr: "js: TypeError:", wantStderr: "TypeError"},
		{name: "ReferenceError", script: `return nope()`, wantErr: "js: ReferenceError: nope is not defined"},
		{name: "ThrowString", script: `throw "str"`, wantErr: "js: str"},
		{name: "LineNumber", script: "const a = 1;\nconst b = 2;\nthrow new Error('line3')", wantErr: "js: Error: line3 (script line 3)", wantStderr: "script:3"},
		{name: "ThrowStringNoStack", script: "\nthrow 'str'", wantErr: "js: str"},
		{name: "RejectedPromise", script: "return Promise.reject(new Error('nope'))", wantErr: "js: Error: nope"},
		{name: "AwaitRejected", script: "\nawait Promise.reject(new TypeError('later'))", wantErr: "js: TypeError: later"},
		{name: "PendingPromise", script: "return new Promise(() => {})", wantErr: "promise that never settled"},
		{name: "FirstLineColumn", script: "null.x", wantStderr: "script:1:6", wantErr: "js: TypeError:"},
		{name: "NativeFrameSkipped", script: "\n[1].map(() => { throw new RangeError('inner') })", wantErr: "js: RangeError: inner (script line 2)"},
		{name: "Recursion", script: `function f() { return f() + 1 } return f()`, wantErr: "Maximum call stack size exceeded"},
		{name: "Circular", script: `const o = {}; o.self = o; return o`, wantErr: "js: TypeError:"},
		{name: "BigInt", script: `return 1n`, wantErr: "js: TypeError:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := run(t, context.Background(), tt.script, nil)
			require.ErrorContains(t, got.err, tt.wantErr)
			if tt.wantStderr != "" {
				assert.Contains(t, got.stderr, tt.wantStderr)
			}
		})
	}
}

func TestJS_Timeout(t *testing.T) {
	t.Parallel()

	start := time.Now()
	got := run(t, context.Background(), `while (true) {}`, map[string]any{"timeout": "100ms"})
	require.EqualError(t, got.err, "js: timeout after 100ms; raise with.timeout to allow longer scripts")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestJS_ContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := run(t, ctx, `while (true) {}`, nil)
	require.ErrorContains(t, got.err, "js: cancelled")
}

func TestJS_Stop(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		stop func(exec *js) error
	}{
		{name: "Kill", stop: func(exec *js) error { return exec.Kill(os.Interrupt) }},
		{name: "Stop", stop: func(exec *js) error { return exec.Stop(cmdutil.TerminationIntent{}) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			created, err := newJS(context.Background(), newStep(`while (true) {}`, nil))
			require.NoError(t, err)
			exec := created.(*js)
			exec.SetStdout(&bytes.Buffer{})
			exec.SetStderr(&bytes.Buffer{})

			errCh := make(chan error, 1)
			go func() { errCh <- exec.Run(context.Background()) }()
			time.Sleep(50 * time.Millisecond)
			require.NoError(t, tt.stop(exec))

			select {
			case err := <-errCh:
				require.EqualError(t, err, "js: stopped")
			case <-time.After(5 * time.Second):
				t.Fatal("script did not stop")
			}
		})
	}

	t.Run("KillBeforeRun", func(t *testing.T) {
		t.Parallel()
		created, err := newJS(context.Background(), newStep(`while (true) {}`, nil))
		require.NoError(t, err)
		require.NoError(t, created.Kill(os.Interrupt))
		require.EqualError(t, created.Run(context.Background()), "js: stopped")
	})
}
