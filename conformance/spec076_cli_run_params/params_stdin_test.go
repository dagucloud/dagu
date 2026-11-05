// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec076_cli_run_params_test checks Spec 076: CLI Run Parameter Input.
package spec076_cli_run_params_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	stdinDAGName   = "params_stdin"
	stdinDAGFile   = stdinDAGName + ".yaml"
	stdinSizeLimit = 1 << 20
)

type stdinRun struct {
	DAGRunID string `json:"dagRunId"`
	Status   string `json:"status"`
	Params   string `json:"params"`
}

type stdinPrecedenceCase struct {
	name  string
	flags []string
	dash  []string
	want  string
}

var stdinPrecedenceCases = []stdinPrecedenceCase{
	{name: "Inherited", want: "default"},
	{name: "Disabled", flags: []string{"--params-stdin=false"}, want: "default"},
	{name: "Flag", flags: []string{"--params-stdin", "--params=value=flag"}, want: "flag"},
	{name: "EmptyFlag", flags: []string{"--params-stdin", "--params="}, want: "default"},
	{name: "Dash", flags: []string{"--params-stdin", "--params=value=flag"}, dash: []string{"--", "value=dash"}, want: "dash"},
	{name: "EmptyDash", flags: []string{"--params-stdin", "--params=value=flag"}, dash: []string{"--"}, want: "default"},
}

func TestParamsStdinValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "Positional", input: "from-stdin\n", want: "from-stdin"},
		{name: "Named", input: "value=from-stdin\n", want: "from-stdin"},
		{name: "JSON", input: `{"value":"from-stdin"}`, want: "from-stdin"},
		{name: "JSONArray", input: `["from-stdin"]`, want: "from-stdin"},
		{name: "JSONInteger", input: `{"value":98765432109876543}`, want: "98765432109876543"},
		{name: "JSONDecimal", input: `[1.10]`, want: "1.10"},
		{name: "JSONObject", input: `{"value":{"ratio":1.10,"tag":"<a&b>"}}`, want: `{"ratio":1.10,"tag":"<a&b>"}`},
		{name: "Quoted", input: `"hello world"`, want: "hello world"},
		{name: "Spaced", input: `" hello world "`, want: " hello world "},
		{name: "NamedQuoted", input: `value=" hello world "`, want: " hello world "},
		{name: "EscapedQuotes", input: `"say \"hello\""`, want: `say "hello"`},
		{name: "QuotedEquals", input: `"a=b"`, want: "a=b"},
		{name: "QuotedAssignments", input: `"a=b bare c=\"x y\""`, want: `a=b bare c="x y"`},
		{name: "NamedAssignments", input: `value="a=b bare c=\"x y\""`, want: `a=b bare c="x y"`},
		{name: "Backslashes", input: `"C:\\Users\\name"`, want: `C:\Users\name`},
		{name: "LiteralBackslashN", input: `"a\\nb"`, want: `a\nb`},
		{name: "EscapedMultiline", input: `"line1\nline2\tend"`, want: "line1\nline2\tend"},
		{name: "Multiline", input: "\"line1\nline2\"", want: "line1\nline2"},
		{name: "MixedMultiline", input: "\"line1\nline2\\nend\\t\\\\path\"", want: "line1\nline2\nend\t\\path"},
		{name: "NamedMultiline", input: "value=\"line1\nline2\\nend\\t\\\\path\"", want: "line1\nline2\nend\t\\path"},
		{name: "MultilineQuote", input: "\"line1\nline2\\\"\"", want: "line1\nline2\""},
		{name: "LineContinuation", input: "\"echo one \\\n  two\"", want: "echo one \\\n  two"},
		{name: "NamedLineContinuation", input: "value=\"echo one \\\n  two\"", want: "echo one \\\n  two"},
		{name: "OddBackslashes", input: "\"line1 " + strings.Repeat(`\`, 3) + "\nline2\"", want: "line1 " + strings.Repeat(`\`, 3) + "\nline2"},
		{name: "EvenBackslashes", input: "\"line1 " + strings.Repeat(`\`, 2) + "\nline2\"", want: "line1 \\\nline2"},
		{name: "MixedLineContinuation", input: "\"line1 " + strings.Repeat(`\`, 2) + "\nline2 \\\nline3\\nend\"", want: "line1 " + strings.Repeat(`\`, 2) + "\nline2 \\\nline3\\nend"},
		{name: "JSONEscapes", input: `{"value":"say \"hello\"\nC:\\Users"}`, want: "say \"hello\"\nC:\\Users"},
		{name: "Unicode", input: `"こんにちは 🌍"`, want: "こんにちは 🌍"},
		{name: "LiteralShell", input: "\"$(printf changed) `printf changed` $HOME 'quoted'\"", want: "$(printf changed) `printf changed` $HOME 'quoted'"},
		{name: "EmptyValue", input: `""`, want: ""},
		{name: "EmptyInput", want: "default"},
		{name: "Whitespace", input: " \n\t\r\n", want: "default"},
	}
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					dagu := harness.NewRunner(t)
					env := stdinEnv(t)
					result := dagu.RunWithStdin(env, strings.NewReader(tc.input),
						command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile)
					result.ExpectExitCode(0)
					expectStdinRun(t, dagu, env, command, tc.want)
				})
			}
		})
	}
}

// Legacy YAML defaults and explicit flags retain shell line continuations
// without enabling stdin parameter input.
func TestParamsLineContinuation(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			for _, source := range []string{"Default", "Flag"} {
				t.Run(source, func(t *testing.T) {
					t.Parallel()
					dagu := harness.NewRunner(t)
					env := stdinEnv(t)
					value := "echo one \\\n  two"
					args := []string{command, "--run-id=" + stdinRunID(t)}
					if source == "Flag" {
						value = "echo three \\\n  four"
						args = append(args, "--params=value=\""+value+"\"")
					}
					args = append(args, "params_stdin_continuation.yaml")
					dagu.RunWithEnv(env, args...).ExpectExitCode(0)
					expectStdinRun(t, dagu, env, command, value)
					if command == "start" {
						want := "one two\n"
						if source == "Flag" {
							want = "three four\n"
						}
						dagu.ExpectFileContent("params_continuation.out", want)
					}
				})
			}
		})
	}
}

// Real file descriptors expose whether a command consumed its caller's input,
// which must remain available to shell loops and higher-precedence sources.
func TestParamsStdinPrecedence(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			for _, tc := range stdinPrecedenceCases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					dagu := harness.NewRunner(t)
					env := stdinEnv(t)
					const input = "value=stdin\nnext-workflow\n"
					stdin, writer, err := os.Pipe()
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, stdin.Close()) })
					_, err = io.WriteString(writer, input)
					require.NoError(t, err)
					require.NoError(t, writer.Close())

					dagu.RunWithStdin(env, stdin, stdinPrecedenceArgs(t, command, tc)...).ExpectExitCode(0)
					expectStdinRun(t, dagu, env, command, tc.want)
					remaining, err := io.ReadAll(stdin)
					require.NoError(t, err)
					require.Equal(t, input, string(remaining))
				})
			}
		})
	}
}

// Unselected input remains unread even when reading would block or fail.
func TestParamsStdinUnusedInput(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			for _, source := range []string{"OpenPipe", "Oversized", "Unreadable"} {
				t.Run(source, func(t *testing.T) {
					t.Parallel()
					for _, tc := range stdinPrecedenceCases {
						t.Run(tc.name, func(t *testing.T) {
							t.Parallel()
							dagu := harness.NewRunner(t)
							env := stdinEnv(t)
							var stdin *os.File
							var err error
							switch source {
							case "OpenPipe":
								var writer *os.File
								stdin, writer, err = os.Pipe()
								require.NoError(t, err)
								t.Cleanup(func() { require.NoError(t, writer.Close()) })
							case "Oversized":
								dagu.WriteFile("stdin.txt", strings.Repeat("x", stdinSizeLimit+1))
								stdin, err = os.Open(dagu.ProjectPath("stdin.txt")) // #nosec G304 -- isolated test input.
							case "Unreadable":
								stdin = unreadableStdin(t, dagu)
							}
							require.NoError(t, err)
							t.Cleanup(func() { require.NoError(t, stdin.Close()) })
							dagu.RunWithStdin(env, stdin, stdinPrecedenceArgs(t, command, tc)...).ExpectExitCode(0)
							expectStdinRun(t, dagu, env, command, tc.want)
							if source == "Oversized" {
								position, err := stdin.Seek(0, io.SeekCurrent)
								require.NoError(t, err)
								require.Zero(t, position)
							}
						})
					}
				})
			}
		})
	}
}

func TestParamsStdinShellLoop(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			dagu.WriteFile("stdin_loop.txt", "first\nsecond\n")
			dagu.RunWithEnv(env, "start", "--run-id="+stdinRunID(t),
				"--params=command="+command+" prefix="+stdinRunID(t), "params_stdin_loop.yaml").ExpectExitCode(0)
			dagu.ExpectFileContent("stdin_loop.out", "first\nsecond\n")
			result := dagu.RunWithEnv(env, "history", "--format=json", "--run-id="+stdinRunID(t), stdinDAGName)
			result.ExpectExitCode(0)
			var runs []stdinRun
			require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &runs))
			status := "queued"
			if command == "start" {
				status = "succeeded"
			}
			require.ElementsMatch(t, []stdinRun{
				{DAGRunID: stdinRunID(t) + "-first", Status: status, Params: "value=default"},
				{DAGRunID: stdinRunID(t) + "-second", Status: status, Params: "value=default"},
			}, runs)
		})
	}
}

func TestParamsStdinFragments(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			stdin, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() { _ = stdin.Close() })
			t.Cleanup(func() { _ = writer.Close() })
			// Exceed pipe capacity so the input cannot arrive in a single read.
			prefix := strings.Repeat(" ", stdinSizeLimit/2)
			const suffix = `hello \"world\"\nこんにちは 🌍"}`
			written := make(chan error, 1)
			go func() {
				_, writeErr := io.WriteString(writer, prefix+`{"value":"`)
				for _, b := range []byte(suffix) {
					if writeErr != nil {
						break
					}
					if _, err := writer.Write([]byte{b}); err != nil {
						writeErr = err
						break
					}
				}
				if err := writer.Close(); writeErr == nil {
					writeErr = err
				}
				written <- writeErr
			}()
			result := dagu.RunWithStdin(env, stdin, command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile)
			require.NoError(t, stdin.Close())
			result.ExpectExitCode(0)
			require.NoError(t, <-written)
			expectStdinRun(t, dagu, env, command, "hello \"world\"\nこんにちは 🌍")
		})
	}
}

func TestParamsStdinReadError(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			stdin := unreadableStdin(t, dagu)
			t.Cleanup(func() { require.NoError(t, stdin.Close()) })
			result := dagu.RunWithStdin(env, stdin, command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("failed to read params from stdin")
			require.Empty(t, stdinHistory(t, dagu, env))
			dagu.ExpectNoFile("params_stdin.out")
		})
	}
}

// The size limit applies before whitespace trimming and before run admission.
func TestParamsStdinLimit(t *testing.T) {
	t.Parallel()

	const params = "value=boundary"
	input := strings.Repeat(" ", stdinSizeLimit-len(params)) + params
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			for _, size := range []int{stdinSizeLimit, stdinSizeLimit + 1} {
				name := "AtLimit"
				if size > stdinSizeLimit {
					name = "OverLimit"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					dagu := harness.NewRunner(t)
					env := stdinEnv(t)
					dagu.WriteFile("stdin.txt", input+strings.Repeat(" ", size-stdinSizeLimit))
					stdin, err := os.Open(dagu.ProjectPath("stdin.txt")) // #nosec G304 -- isolated test input.
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, stdin.Close()) })
					result := dagu.RunWithStdin(env, stdin, command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile)
					if size == stdinSizeLimit {
						result.ExpectExitCode(0)
						expectStdinRun(t, dagu, env, command, "boundary")
						return
					}
					result.ExpectNonZeroExitCode()
					result.ExpectStderrContains("params from stdin exceed", "1048576 byte limit")
					require.Empty(t, stdinHistory(t, dagu, env))
					dagu.ExpectNoFile("params_stdin.out")
				})
			}
		})
	}
}

func TestParamsStdinCharacterDevice(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			stdin, err := os.Open(os.DevNull)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stdin.Close()) })
			dagu.RunWithStdin(env, stdin, command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile).ExpectExitCode(0)
			expectStdinRun(t, dagu, env, command, "default")
		})
	}
}

func TestParamsStdinLimitOpenPipe(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			stdin, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, writer.Close())
				require.NoError(t, stdin.Close())
			})
			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(writer, strings.Repeat(" ", stdinSizeLimit+1))
				written <- err
			}()
			// Leave the writer open: oversize rejection must not wait for EOF.
			result := dagu.RunWithStdin(env, stdin, command, "--params-stdin", "--run-id="+stdinRunID(t), stdinDAGFile)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("params from stdin exceed", "1048576 byte limit")
			require.NoError(t, <-written)
			require.Empty(t, stdinHistory(t, dagu, env))
			dagu.ExpectNoFile("params_stdin.out")
		})
	}
}

func TestParamsStdinFromRunID(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"Populated", "Empty", "OpenPipe", "Disabled"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			env := stdinEnv(t)
			var stdin io.Reader = strings.NewReader("")
			sourceID := "source-" + strings.TrimPrefix(stdinRunID(t), "Test")
			flag := "--params-stdin"
			switch input {
			case "Populated":
				stdin = strings.NewReader("value=stdin")
			case "OpenPipe":
				reader, writer, err := os.Pipe()
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, writer.Close())
					require.NoError(t, reader.Close())
				})
				stdin = reader
			case "Disabled":
				flag = "--params-stdin=false"
				dagu.RunWithEnv(env, "start", "--run-id="+sourceID, "--params=value=saved", stdinDAGFile).ExpectExitCode(0)
			}
			result := dagu.RunWithStdin(env, stdin,
				"start", flag, "--from-run-id="+sourceID, "--run-id="+stdinRunID(t), stdinDAGFile)
			if input == "Disabled" {
				result.ExpectExitCode(0)
				expectStdinRun(t, dagu, env, "start", "saved")
				return
			}
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("parameters cannot be provided when using --from-run-id")
			require.Empty(t, stdinHistory(t, dagu, env))
			dagu.ExpectNoFile("params_stdin.out")
		})
	}
}

// Keep follow-up commands on the same run store and fixture catalog.
func stdinEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
		"DAGU_DAGS_DIR=.",
	}
}

func expectStdinRun(t *testing.T, dagu *harness.Runner, env []string, command, value string) {
	t.Helper()

	runs := stdinHistory(t, dagu, env)
	require.Len(t, runs, 1)
	status := "queued"
	if command == "start" {
		status = "succeeded"
		dagu.ExpectFileContent("params_stdin.out", value+"\n")
	}
	require.Equal(t, stdinRun{DAGRunID: stdinRunID(t), Status: status, Params: "value=" + value}, runs[0])
}

func stdinHistory(t *testing.T, dagu *harness.Runner, env []string) []stdinRun {
	t.Helper()

	result := dagu.RunWithEnv(env, "history", "--format=json", "--run-id="+stdinRunID(t), stdinDAGName)
	result.ExpectExitCode(0)
	var runs []stdinRun
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &runs))
	return runs
}

func stdinPrecedenceArgs(t *testing.T, command string, tc stdinPrecedenceCase) []string {
	t.Helper()
	args := append([]string{command, "--run-id=" + stdinRunID(t)}, tc.flags...)
	args = append(args, stdinDAGFile)
	return append(args, tc.dash...)
}

// A write-only file can be inspected but cannot be read on Unix or Windows.
func unreadableStdin(t *testing.T, dagu *harness.Runner) *os.File {
	t.Helper()
	stdin, err := os.OpenFile(dagu.ProjectPath("unreadable.txt"), os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304 -- isolated test input.
	require.NoError(t, err)
	return stdin
}

// Run IDs distinguish process sockets even across isolated DAGU_HOME values.
func stdinRunID(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "-")
}
