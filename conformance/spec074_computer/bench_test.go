// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec074_computer_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// The desktop bench runs desktop tasks with a real model against the elements
// window, beside a window of another app that holds a look-alike 保存 button,
// several times each, and reports how each run went: whether it succeeded,
// how many actions and tokens it took, how long, and whether it pressed the
// other app's button. It is not part of CI: it needs an interactive macOS or
// Windows desktop nobody uses, and a model, read from
//
//	DAGU_DESKTOP_BENCH=1
//	DAGU_BENCH_PROVIDER and DAGU_BENCH_MODEL, with the provider's key in its
//	  usual environment variable, such as OPENROUTER_API_KEY
//	DAGU_BENCH_RUNS, the runs of each task, 3 by default
//	DAGU_BENCH_TASKS, a comma-separated subset of the tasks below
//	DAGU_BENCH_OUT, a file the results are written to as JSON
//
// Each run asks the model (ai: every_run), so replays never hide how the
// model does. DAGU_BIN names the dagu binary to run, as for every
// conformance test. A whole bench can outlast go test's default timeout of
// ten minutes, -v shows the results of a bench that passes, and -count=1
// runs the model again rather than reuse a cached result:
//
//	DAGU_BIN=/path/to/dagu go test -v -count=1 -timeout 120m -run TestComputerDesktopBench ./conformance/spec074_computer

// benchTimeout bounds one run of a task, longer than a desktop command's,
// since a weak model may take minutes.
const benchTimeout = 8 * time.Minute

// benchKeys are the environment variables a model's key may be in, passed
// through to the runs.
var benchKeys = []string{"OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"}

// benchTask is one task: the operations its step runs after the elements
// window is in front, and, for a task that reads, the output it must
// publish.
type benchTask struct {
	name   string
	do     string
	output string
	want   string
}

var benchTasks = []benchTask{
	{name: "choose", do: `
        - act: 支払方法を「クレジット」にする
        - expect: {text: "支払方法: クレジット", within: 10s}`},
	{name: "fill", do: `
        - act: 金額に 1200 を、支払情報の金額に 3400 を入力し、「同意する」にチェックを入れる
        - expect: {text: "1200", within: 10s}
        - expect: {text: "3400", within: 10s}
        - expect: {text: "同意: True", within: 10s}`},
	{name: "read", do: `
        - extract:
            instruction: 表の「みかん」の行の金額
            schema: {type: object, properties: {amount: {type: string}}}`, output: "amount", want: "3,400"},
	{name: "press", do: `
        - act: 支払情報の「保存」を押す
        - expect: {text: 保存しました, within: 10s}`},
}

// benchRun is how one run of a task went.
type benchRun struct {
	Task    string  `json:"task"`
	Run     int     `json:"run"`
	Success bool    `json:"success"`
	Reason  string  `json:"reason,omitempty"`
	Actions int     `json:"actions"`
	Tokens  int     `json:"tokens"`
	Seconds float64 `json:"seconds"`
	// Strayed reports a run that pressed the other app's button.
	Strayed bool `json:"strayed"`
}

func TestComputerDesktopBench(t *testing.T) {
	if os.Getenv("DAGU_DESKTOP_BENCH") != "1" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows") {
		t.Skip("set DAGU_DESKTOP_BENCH=1 on an interactive macOS or Windows desktop")
	}
	provider, model := os.Getenv("DAGU_BENCH_PROVIDER"), os.Getenv("DAGU_BENCH_MODEL")
	require.NotEmpty(t, provider, "DAGU_BENCH_PROVIDER names the model's provider")
	require.NotEmpty(t, model, "DAGU_BENCH_MODEL names the model")
	runs := 3
	if n, err := strconv.Atoi(os.Getenv("DAGU_BENCH_RUNS")); err == nil && n > 0 {
		runs = n
	}
	var env, secrets []string
	for _, key := range benchKeys {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
			secrets = append(secrets, fmt.Sprintf("  - {name: %s, provider: env, key: %s}", key, key))
		}
	}
	only := strings.Split(os.Getenv("DAGU_BENCH_TASKS"), ",")

	var results []benchRun
	for _, task := range benchTasks {
		if only[0] != "" && !slices.Contains(only, task.name) {
			continue
		}
		for run := 1; run <= runs; run++ {
			// Each run is a subtest of its own, so its windows close before
			// the next run opens fresh ones. A run that ends the subtest, as
			// a command that times out does, still counts as a failure.
			t.Run(fmt.Sprintf("%s/%d", task.name, run), func(t *testing.T) {
				began := time.Now()
				result := benchRun{Task: task.name, Reason: "the run did not finish"}
				defer func() {
					result.Run = run
					if result.Seconds == 0 {
						result.Seconds = time.Since(began).Seconds()
					}
					t.Logf("success=%v actions=%d tokens=%d %.0fs strayed=%v %s", result.Success, result.Actions, result.Tokens, result.Seconds, result.Strayed, result.Reason)
					results = append(results, result)
				}()
				result = runBenchTask(t, task, provider, model, env, secrets)
			})
		}
	}
	t.Log("\n" + benchTable(results))
	if out := os.Getenv("DAGU_BENCH_OUT"); out != "" {
		data, err := json.MarshalIndent(map[string]any{"provider": provider, "model": model, "system": runtime.GOOS, "runs": results}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(out, data, 0o600))
	}
}

// runBenchTask runs one task once on fresh windows.
// The model's key reaches the step as a secret read from the environment,
// never written into the definition.
func runBenchTask(t *testing.T, task benchTask, provider, model string, env, secrets []string) benchRun {
	t.Helper()
	dagu := harness.NewRunner(t).WithCommandTimeout(benchTimeout)
	marker := dagu.ProjectPath("distractor-pressed")
	startFixture(t, dagu, "distractor", marker)
	startFixture(t, dagu, "elements")
	dagu.WriteFile("task.yaml", fmt.Sprintf(`working_dir: .
secrets:
%s
llm:
  provider: %s
  model: %s
steps:
  - id: task
    action: computer.run
    stdout: task.json
    with:
      idle: "0"
      ai: every_run
      screenshots: never
      maximize: false
      do:
        - expect: {window: "Dagu elements test", within: 30s}%s
`, strings.Join(secrets, "\n"), provider, model, task.do))

	began := time.Now()
	result := dagu.RunWithEnv(env, "start", "task.yaml")
	run := benchRun{Task: task.name, Seconds: time.Since(began).Seconds()}
	_, err := os.Stat(marker)
	run.Strayed = err == nil
	run.Actions, run.Tokens, run.Reason = readStepLog(result.Stdout())
	run.Success = result.ExitCode() == 0 && !run.Strayed
	if run.Strayed {
		run.Reason = "pressed the other app's 保存"
	}
	if run.Success && task.output != "" {
		var outputs map[string]any
		data, _ := os.ReadFile(dagu.ProjectPath("task.json"))
		if json.Unmarshal(data, &outputs) != nil || fmt.Sprint(outputs[task.output]) != task.want {
			run.Success, run.Reason = false, fmt.Sprintf("read %v, not %q", outputs[task.output], task.want)
		}
	}
	return run
}

// startFixture starts a fixture window, elements or distractor, for the rest
// of the test, and waits for the elements window to be in front.
func startFixture(t *testing.T, dagu *harness.Runner, name string, args ...string) {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("/usr/bin/osascript", append([]string{"-l", "JavaScript", dagu.ProjectPath(name + ".js")}, args...)...)
	} else {
		if len(args) > 0 {
			args = []string{"-Out", args[0]}
		}
		cmd = exec.Command("powershell.exe", append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", dagu.ProjectPath(name + ".ps1")}, args...)...)
	}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if name != "elements" {
		return
	}
	var front string
	require.Eventually(t, func() bool {
		var out struct {
			Window elementJSON `json:"window"`
		}
		if json.Unmarshal([]byte(dagu.Run("computer", "elements", "--format", "json", "--limit", "1").Stdout()), &out) != nil {
			return false
		}
		front = out.Window.Name
		if front == elementsWindowTitle {
			return true
		}
		if runtime.GOOS == "windows" {
			clickWindow(t)
		}
		return false
	}, 30*time.Second, time.Second, "the elements window did not come to the front; %q is there", &front)
}

var (
	// stepLog finds the step's stderr log in what dagu start printed.
	stepLog = regexp.MustCompile(`stderr: (\S+task\.[^\s]+\.err)`)
	// operationLine is an operation's outcome: its status and tokens.
	operationLine = regexp.MustCompile(`^\[\d+/\d+\] \w+ .*\((completed|failed|skipped)(?:, (\d+) tokens)?`)
	// actionLine is one action the step carried out on the desktop.
	actionLine = regexp.MustCompile(`^\[\d+/\d+\]\s{3}(click|double_click|right_click|type|key|scroll|drag|move|invoke|toggle|select|expand|focus|set_value|replay by element)\b`)
)

// readStepLog counts the actions and tokens the step's log reports, and the
// reason of its last failed operation.
func readStepLog(stdout string) (actions, tokens int, reason string) {
	match := stepLog.FindStringSubmatch(stdout)
	if match == nil {
		return 0, 0, "the step log was not found"
	}
	data, err := os.ReadFile(filepath.Clean(match[1]))
	if err != nil {
		return 0, 0, err.Error()
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if actionLine.MatchString(line) {
			actions++
		}
		if m := operationLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			tokens += n
			if m[1] == "failed" {
				reason = strings.TrimSpace(line)
			}
		}
	}
	return actions, tokens, reason
}

// benchTable sums the runs up per task: successes, median actions, tokens,
// and seconds, and runs that strayed into the other app.
func benchTable(results []benchRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %8s %8s %8s %8s %8s\n", "task", "success", "actions", "tokens", "seconds", "strayed")
	var tasks []string
	for _, r := range results {
		if !slices.Contains(tasks, r.Task) {
			tasks = append(tasks, r.Task)
		}
	}
	for _, task := range tasks {
		var ok, strayed int
		var actions, tokens, seconds []float64
		for _, r := range results {
			if r.Task != task {
				continue
			}
			if r.Success {
				ok++
			}
			if r.Strayed {
				strayed++
			}
			actions, tokens, seconds = append(actions, float64(r.Actions)), append(tokens, float64(r.Tokens)), append(seconds, r.Seconds)
		}
		fmt.Fprintf(&b, "%-8s %5d/%-2d %8.0f %8.0f %8.0f %8d\n", task, ok, len(actions), median(actions), median(tokens), median(seconds), strayed)
	}
	return b.String()
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[middle-1] + sorted[middle]) / 2
	}
	return sorted[middle]
}
