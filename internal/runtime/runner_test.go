// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/builtin/agentstep"
	"github.com/dagucloud/dagu/internal/runtime/builtin/chat"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shellTestPath(path string) string {
	return filepath.ToSlash(path)
}

func windowsShellTest() bool {
	return os.PathSeparator == '\\'
}

func shellSubstitution(command string) string {
	return "`" + command + "`"
}

func trimmedCounterReadCommand(counterFile string) string {
	if windowsShellTest() {
		return fmt.Sprintf(
			"(& { $content = Get-Content -Raw -LiteralPath %s -ErrorAction SilentlyContinue; if ($null -eq $content) { '' } else { ([string]$content).TrimEnd([char]13, [char]10) } })",
			test.PowerShellQuote(counterFile),
		)
	}
	return fmt.Sprintf("tr -d '\\r\\n' < %s", test.PosixQuote(counterFile))
}

func createEmptyFileCommand(path string) string {
	if windowsShellTest() {
		return fmt.Sprintf("New-Item -ItemType File -Path %s -Force | Out-Null", test.PowerShellQuote(path))
	}
	return fmt.Sprintf(": > %s", test.PosixQuote(path))
}

func fileExistsCommand(path string) string {
	if windowsShellTest() {
		return fmt.Sprintf("if (Test-Path %s) { exit 0 } else { exit 1 }", test.PowerShellQuote(path))
	}
	return fmt.Sprintf("test -f %s", test.PosixQuote(path))
}

func fileMissingCommand(path string) string {
	if windowsShellTest() {
		return fmt.Sprintf("if (-not (Test-Path %s)) { exit 0 } else { exit 1 }", test.PowerShellQuote(path))
	}
	return fmt.Sprintf("test ! -f %s", test.PosixQuote(path))
}

func repeatCounterValueCondition(counterFile string) string {
	if windowsShellTest() {
		return shellSubstitution(fmt.Sprintf(
			"if (Test-Path %s) { [System.IO.File]::ReadAllText(%s).TrimEnd([char]13,[char]10) } else { '' }",
			test.PowerShellQuote(counterFile),
			test.PowerShellQuote(counterFile),
		))
	}
	return shellSubstitution(trimmedCounterReadCommand(counterFile))
}

func repeatExpectedCondition(counterFile, expected string) *core.Condition {
	if windowsShellTest() {
		return &core.Condition{Condition: repeatCounterEqualsCommand(counterFile, expected)}
	}
	return &core.Condition{
		Condition: repeatCounterValueCondition(counterFile),
		Expected:  expected,
	}
}

func repeatConditionMutationTimeout() time.Duration {
	if windowsShellTest() {
		return 90 * time.Second
	}
	return 5 * time.Second
}

func readRepeatCounterValue(t *testing.T, counterFile string) int {
	t.Helper()

	data, err := os.ReadFile(counterFile)
	require.NoError(t, err)

	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)

	return value
}

func repeatCounterEqualsCommand(counterFile, expected string) string {
	if windowsShellTest() {
		return fmt.Sprintf(
			"if ((Test-Path %s) -and ((%s) -eq %s)) { exit 0 } else { exit 1 }",
			test.PowerShellQuote(counterFile),
			trimmedCounterReadCommand(counterFile),
			test.PowerShellQuote(expected),
		)
	}
	return fmt.Sprintf("test \"$(%s)\" = \"%s\"", trimmedCounterReadCommand(counterFile), expected)
}

func repeatCounterSetupScript(counterFile string) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
					$counterFile = %s
					if (Test-Path $counterFile) {
						$COUNT = [int](([string](Get-Content -Raw -Path $counterFile)).TrimEnd([char]13, [char]10))
					} else {
						$COUNT = 0
					}
				`, test.PowerShellQuote(counterFile))
	}
	return fmt.Sprintf(`
					COUNTER_FILE="%s"
					if [ -f "$COUNTER_FILE" ]; then
						COUNT=$(%s)
					else
						COUNT=0
					fi
				`, shellTestPath(counterFile), trimmedCounterReadCommand(counterFile))
}

func repeatCounterScript(counterFile string, emitCount bool) string {
	if windowsShellTest() {
		emitLine := ""
		if emitCount {
			emitLine = "\n\t\t\t\t\tWrite-Output $COUNT"
		}
		return fmt.Sprintf(`
					%s
					$COUNT = $COUNT + 1
					Set-Content -Path $counterFile -Value $COUNT -NoNewline%s
				`, repeatCounterSetupScript(counterFile), emitLine)
	}
	emitLine := ""
	if emitCount {
		emitLine = "\n\t\t\t\t\tprintf '%s\\n' \"$COUNT\""
	}
	return fmt.Sprintf(`
					%s
					COUNT=$((COUNT + 1))
					printf '%%s' "$COUNT" > "$COUNTER_FILE"%s
				`, repeatCounterSetupScript(counterFile), emitLine)
}

func repeatCounterThenSleepScript(counterFile string, sleepAfterCount int, sleepDuration time.Duration) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
					%s
					$COUNT = $COUNT + 1
					Set-Content -Path $counterFile -Value $COUNT -NoNewline
					if ($COUNT -ge %d) {
						%s
					}
				`, repeatCounterSetupScript(counterFile), sleepAfterCount, test.Sleep(sleepDuration))
	}
	return fmt.Sprintf(`
					%s
					COUNT=$((COUNT + 1))
					printf '%%s' "$COUNT" > "$COUNTER_FILE"
					if [ "$COUNT" -ge %d ]; then
						%s
					fi
				`, repeatCounterSetupScript(counterFile), sleepAfterCount, test.Sleep(sleepDuration))
}

func repeatCounterExitCodeScript(counterFile string) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
					$counterFile = %s
					if (Test-Path $counterFile) {
						exit 0
					}
					New-Item -ItemType File -Path $counterFile -Force | Out-Null
					exit 42
				`, test.PowerShellQuote(counterFile))
	}
	return fmt.Sprintf(`if [ -f %[1]s ]; then exit 0; fi; : > %[1]s; exit 42`, shellTestPath(counterFile))
}

func retrySpecificExitCodeScript(counterFile string) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
					$counterFile = %s
					if (-not (Test-Path $counterFile)) {
						Set-Content -Path $counterFile -Value '1' -NoNewline
						exit 42
					}

					$COUNT = ([string](Get-Content -Raw -Path $counterFile)).TrimEnd([char]13, [char]10)
					if ($COUNT -eq '1') {
						Set-Content -Path $counterFile -Value '2' -NoNewline
						exit 100
					}
				`, test.PowerShellQuote(counterFile))
	}
	return fmt.Sprintf(`
					if [ ! -f "%s" ]; then
						printf '%%s' "1" > "%s"
						exit 42
					else
						COUNT=$(tr -d '\r\n' < "%s")
						if [ "$COUNT" -eq "1" ]; then
							printf '%%s' "2" > "%s"
							exit 100
						fi
					fi
				`, shellTestPath(counterFile), shellTestPath(counterFile), shellTestPath(counterFile), shellTestPath(counterFile))
}

func dagRunStatusUnsetScript() string {
	if windowsShellTest() {
		return `if ([string]::IsNullOrEmpty($env:DAG_RUN_STATUS)) { Write-Output unset } else { Write-Output set }`
	}
	return `if [ -z "$DAG_RUN_STATUS" ]; then echo unset; else echo set; fi`
}

func counterThresholdExitScript(counterFile string, threshold, exitAtOrBelow, exitAbove int) string {
	if windowsShellTest() {
		return fmt.Sprintf(`
					%s
					$COUNT = $COUNT + 1
					Set-Content -Path $counterFile -Value $COUNT -NoNewline
					if ($COUNT -le %d) {
						exit %d
					}
					exit %d
				`, repeatCounterSetupScript(counterFile), threshold, exitAtOrBelow, exitAbove)
	}
	return fmt.Sprintf(`
					%s
					COUNT=$((COUNT + 1))
					printf '%%s' "$COUNT" > "$COUNTER_FILE"
					if [ "$COUNT" -le %d ]; then
						exit %d
					fi
					exit %d
				`, repeatCounterSetupScript(counterFile), threshold, exitAtOrBelow, exitAbove)
}

func retryOutputSequenceScript(counterFile string, outputs []string, successAttempt int) string {
	if windowsShellTest() {
		var body strings.Builder
		fmt.Fprintf(&body, "\n\t\t\t\t\t$counterFile = %s\n", test.PowerShellQuote(counterFile))
		body.WriteString("\t\t\t\t\t$attempt = 1\n")
		body.WriteString("\t\t\t\t\tif (Test-Path $counterFile) {\n")
		body.WriteString("\t\t\t\t\t\t$attempt = [int](([string](Get-Content -Raw -Path $counterFile)).TrimEnd([char]13, [char]10)) + 1\n")
		body.WriteString("\t\t\t\t\t}\n")
		body.WriteString("\t\t\t\t\tSet-Content -Path $counterFile -Value $attempt -NoNewline\n")
		body.WriteString("\t\t\t\t\tswitch ($attempt) {\n")
		for i, output := range outputs {
			attempt := i + 1
			exitCode := 1
			if attempt == successAttempt {
				exitCode = 0
			}
			fmt.Fprintf(&body, "\t\t\t\t\t\t%d { Write-Output %s; exit %d }\n", attempt, test.PowerShellQuote(output), exitCode)
		}
		body.WriteString("\t\t\t\t\t\tdefault { exit 1 }\n")
		body.WriteString("\t\t\t\t\t}\n")
		return body.String()
	}

	var body strings.Builder
	fmt.Fprintf(&body, "\n\t\t\t\t\tCOUNTER_FILE=%q\n", shellTestPath(counterFile))
	body.WriteString("\t\t\t\t\tif [ ! -f \"$COUNTER_FILE\" ]; then\n")
	body.WriteString("\t\t\t\t\t\tATTEMPT=1\n")
	body.WriteString("\t\t\t\t\telse\n")
	body.WriteString("\t\t\t\t\t\tATTEMPT=$(($(tr -d '\\r\\n' < \"$COUNTER_FILE\") + 1))\n")
	body.WriteString("\t\t\t\t\tfi\n")
	body.WriteString("\t\t\t\t\tprintf '%s' \"$ATTEMPT\" > \"$COUNTER_FILE\"\n")
	body.WriteString("\t\t\t\t\tcase \"$ATTEMPT\" in\n")
	for i, output := range outputs {
		attempt := i + 1
		exitCode := 1
		if attempt == successAttempt {
			exitCode = 0
		}
		fmt.Fprintf(&body, "\t\t\t\t\t\t%d)\n\t\t\t\t\t\t\techo %q\n\t\t\t\t\t\t\texit %d\n\t\t\t\t\t\t\t;;\n", attempt, output, exitCode)
	}
	body.WriteString("\t\t\t\t\t\t*)\n\t\t\t\t\t\t\texit 1\n\t\t\t\t\t\t\t;;\n")
	body.WriteString("\t\t\t\t\tesac\n")
	return body.String()
}

func TestRunner(t *testing.T) {
	t.Run("SequentialStepsSuccess", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(1))

		// 1 -> 2 -> 3
		plan := r.newPlan(t,
			successStep("1"),
			successStep("2", "1"),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("SequentialStepsWithFailure", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(1))

		// 1 -> 2 -> 3 -> 4
		plan := r.newPlan(t,
			successStep("1"),
			successStep("2", "1"),
			failStep("3", "2"),
			successStep("4", "3"),
		)

		result := plan.assertRun(t, core.Failed)

		// 1, 2, 3 should be executed and 4 should be canceled because 3 failed
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeFailed)
		result.assertNodeStatus(t, "4", core.NodeAborted)
	})
	t.Run("ParallelSteps", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(3))

		// 1,2,3
		plan := r.newPlan(t,
			successStep("1"),
			successStep("2"),
			successStep("3"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("ParallelStepsWithFailure", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 3 -> 4, 2 (fail)
		plan := r.newPlan(t,
			successStep("1"),
			failStep("2"),
			successStep("3", "1"),
			successStep("4", "3"),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeFailed)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
		result.assertNodeStatus(t, "4", core.NodeSucceeded)
	})
	t.Run("SkippedByRetryDependencyAllowsDownstream", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(1))

		plan, err := runtime.NewPlanFromNodes(
			runtime.NewNode(successStep("1"), runtime.NodeState{
				Status:         core.NodeSkipped,
				SkippedByRetry: true,
			}),
			runtime.NewNode(successStep("2", "1"), runtime.NodeState{
				Status: core.NodeNotStarted,
			}),
		)
		require.NoError(t, err)

		result := planHelper{
			testHelper: r,
			Plan:       plan,
			workDir:    t.TempDir(),
		}.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSkipped)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
	})
	t.Run("OrdinarySkippedDependencyStillSkipsDownstream", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(1))

		plan, err := runtime.NewPlanFromNodes(
			runtime.NewNode(successStep("1"), runtime.NodeState{
				Status: core.NodeSkipped,
			}),
			runtime.NewNode(successStep("2", "1"), runtime.NodeState{
				Status: core.NodeNotStarted,
			}),
		)
		require.NoError(t, err)

		result := planHelper{
			testHelper: r,
			Plan:       plan,
			workDir:    t.TempDir(),
		}.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSkipped)
		result.assertNodeStatus(t, "2", core.NodeSkipped)
	})
	t.Run("ComplexCommand", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t, withMaxActiveRuns(1))

		plan := r.newPlan(t,
			newStep("1",
				withCommand("df / | awk 'NR==2 {exit $4 > 5000 ? 0 : 1}'"),
			))

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
	})
	t.Run("ContinueOnFailure", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (fail) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2",
				withDepends("1"),
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)

		// 1, 2, 3 should be executed even though 2 failed
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeFailed)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("ContinueOnSkip", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (skip) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2",
				withDepends("1"),
				withCommand("false"),
				withPrecondition(&core.Condition{
					Condition: "`echo 1`",
					Expected:  "0",
				}),
				withContinueOn(core.ContinueOn{
					Skipped: true,
				}),
			),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSkipped)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("ContinueOnExitCode", func(t *testing.T) {
		r := setupRunner(t)

		// 1 (exit code 1) -> 2
		plan := r.newPlan(t,
			newStep("1",
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					ExitCode: []int{1},
				}),
			),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)

		// 1, 2 should be executed even though 1 failed
		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
	})
	t.Run("ContinueOnOutputStdout", func(t *testing.T) {
		r := setupRunner(t)

		// 1 (exit code 1) -> 2
		plan := r.newPlan(t,
			newStep("1",
				withCommand("echo test_output; false"), // stdout: test_output
				withContinueOn(core.ContinueOn{
					Output: []string{
						"test_output",
					},
				}),
			),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)

		// 1, 2 should be executed even though 1 failed
		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
	})
	t.Run("ContinueOnOutputStderr", func(t *testing.T) {
		r := setupRunner(t)
		command := test.JoinLines(
			test.Stderr("test_output"),
			test.Output("test_output"),
			"exit 1",
		)

		// 1 (exit code 1) -> 2
		plan := r.newPlan(t,
			newStep("1",
				withCommand(command), // write to stderr and stdout
				withContinueOn(core.ContinueOn{
					Output: []string{
						"test_output",
					},
				}),
			),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)

		// Step 1 fails but matches continueOn output, allowing step 2 to run
		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		stderrData, err := os.ReadFile(node.GetStderr())
		require.NoError(t, err)
		assert.Contains(t, string(stderrData), "test_output")
	})
	t.Run("ContinueOnOutputRegexp", func(t *testing.T) {
		r := setupRunner(t)

		// 1 (exit code 1) -> 2
		plan := r.newPlan(t,
			newStep("1",
				withCommand("echo test_output; false"), // stdout: test_output
				withContinueOn(core.ContinueOn{
					Output: []string{
						"re:^test_[a-z]+$",
					},
				}),
			),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)

		// 1, 2 should be executed even though 1 failed
		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
	})
	t.Run("ContinueOnMarkSuccess", func(t *testing.T) {
		r := setupRunner(t)

		// 1 (exit code 1) -> 2
		plan := r.newPlan(t,
			newStep("1",
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					ExitCode:    []int{1},
					MarkSuccess: true,
				}),
			),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.Succeeded)

		// 1, 2 should be executed even though 1 failed
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
	})
	t.Run("Cancel", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (cancel when running) -> 3 (should not be executed)
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2", withDepends("1"), withCommand("sleep 0.5")),
			failStep("3", "2"),
		)

		go func() {
			waitForNodeStatus(plan.Plan, "2", core.NodeRunning, 5*time.Second)
			plan.cancel(t)
		}()

		result := plan.assertRun(t, core.Aborted)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeAborted)
		result.assertNodeStatus(t, "3", core.NodeNotStarted)
	})
	t.Run("Timeout", func(t *testing.T) {
		dagTimeout := 500 * time.Millisecond
		secondSleep := 500 * time.Millisecond
		if windowsShellTest() {
			dagTimeout = 3 * time.Second
			secondSleep = 5 * time.Second
		}

		r := setupRunner(t, withTimeout(dagTimeout))

		// 1 -> 2 (timeout) -> 3 (should not be executed)
		plan := r.newPlan(t,
			newStep("1", withCommand("exit 0")),
			newStep("2", withCommand(test.Sleep(secondSleep)), withDepends("1")),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Failed)

		// 1 should be executed and 2 should be canceled because of timeout
		// 3 should not be executed and should be canceled
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeAborted)
		result.assertNodeStatus(t, "3", core.NodeAborted)
	})
	t.Run("RetryPolicyFail", func(t *testing.T) {
		const file = "flag_test_retry_fail"

		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1",
				withCommand(fileExistsCommand(file)),
				withRetryPolicy(2, 0),
			),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")
		require.Equal(t, 2, node.State().RetryCount) // 2 retry
	})
	t.Run("RetryWithScript", func(t *testing.T) {
		r := setupRunner(t)
		tmpDir := t.TempDir()
		testFile := path.Join(tmpDir, "testfile.txt")

		plan := r.newPlan(t,
			newStep("1",
				withScript(func() string {
					if windowsShellTest() {
						return fmt.Sprintf(`
							if (-not (Test-Path %s)) {
								%s
								exit 1
							}
							exit 0
						`, test.PowerShellQuote(testFile), createEmptyFileCommand(testFile))
					}
					return fmt.Sprintf(`
						if [ ! -f %s ]; then
							%s
							exit 1
						fi
						exit 0
					`, test.PosixQuote(testFile), createEmptyFileCommand(testFile))
				}()),
				withRetryPolicy(1, time.Millisecond*50),
			),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		require.Equal(t, 1, node.State().DoneCount)  // 1 successful execution
		require.Equal(t, 1, node.State().RetryCount) // 1 retry
	})
	t.Run("RetryPolicySuccess", func(t *testing.T) {
		file := filepath.Join(
			os.TempDir(), fmt.Sprintf("flag_test_retry_success_%s", uuid.Must(uuid.NewV7()).String()),
		)
		t.Cleanup(func() {
			_ = os.Remove(file)
		})

		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1",
				withCommand(fileExistsCommand(file)),
				withRetryPolicy(3, time.Millisecond*50),
			),
		)

		waitTimeout := 5 * time.Second
		if windowsShellTest() {
			waitTimeout = 30 * time.Second
		}
		fileReady := make(chan error, 1)
		go func() {
			// Wait until at least one retry has happened before creating the file
			// so that the first attempt and first retry both fail.
			deadline := time.After(waitTimeout)
			for {
				select {
				case <-deadline:
					fileReady <- fmt.Errorf("timed out waiting for retry count to increment")
					return
				default:
				}
				node := plan.GetNodeByName("1")
				if node != nil && node.State().RetryCount > 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}

			// Create file during the retry interval
			f, err := os.Create(file)
			if err != nil {
				fileReady <- err
				return
			}
			if err := f.Close(); err != nil {
				fileReady <- err
				return
			}
			fileReady <- nil
		}()

		result := plan.assertRun(t, core.Succeeded)
		require.NoError(t, <-fileReady)

		// Check if the retry is successful
		state := result.nodeByName(t, "1").State()
		assert.Equal(t, 1, state.DoneCount)
		assert.Greater(t, state.RetryCount, 0)
		assert.NotEmpty(t, state.RetriedAt)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
	})
	t.Run("PreconditionMatch", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// 1 -> 2 (precondition match) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2", withCommand("echo 2"),
				withPrecondition(&core.Condition{
					Condition: "1",
					Expected:  "1",
				}),
			),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("PreconditionNotMatch", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (precondition not match) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2", withCommand("echo 2"),
				withPrecondition(&core.Condition{
					Condition: "1",
					Expected:  "0",
				})),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		// 1 should be executed and 2, 3 should be skipped
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSkipped)
		result.assertNodeStatus(t, "3", core.NodeSkipped)
	})
	t.Run("PreconditionWithCommandMet", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (precondition not match) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2", withCommand("echo 2"),
				withPrecondition(&core.Condition{
					Condition: "exit 0",
				})),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
	})
	t.Run("PreconditionWithCommandNotMet", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (precondition not match) -> 3
		plan := r.newPlan(t,
			successStep("1"),
			newStep("2", withCommand("echo 2"),
				withPrecondition(&core.Condition{
					Condition: "exit 1",
				})),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Succeeded)

		// 1 should be executed and 2, 3 should be skipped
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSkipped)
		result.assertNodeStatus(t, "3", core.NodeSkipped)
	})
	t.Run("OnExitHandler", func(t *testing.T) {
		r := setupRunner(t, withOnExit(successStep("onExit")))

		plan := r.newPlan(t, successStep("1"))

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "onExit", core.NodeSucceeded)
	})
	t.Run("OnExitHandlerFail", func(t *testing.T) {
		r := setupRunner(t, withOnExit(failStep("onExit")))

		plan := r.newPlan(t, successStep("1"))

		// Overall status should be error because onExit failed
		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "onExit", core.NodeFailed)
	})
	t.Run("OnAbortHandler", func(t *testing.T) {
		r := setupRunner(t, withOnAbort(successStep("onAbort")))

		plan := r.newPlan(t,
			newStep("1", withCommand("sleep 0.5")),
		)

		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			plan.signal(syscall.SIGTERM)
		}()

		result := plan.assertRun(t, core.Aborted)

		result.assertNodeStatus(t, "1", core.NodeAborted)
		result.assertNodeStatus(t, "onAbort", core.NodeSucceeded)
	})
	t.Run("OnSuccessHandler", func(t *testing.T) {
		r := setupRunner(t, withOnSuccess(successStep("onSuccess")))

		plan := r.newPlan(t, successStep("1"))

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "onSuccess", core.NodeSucceeded)
	})
	t.Run("OnFailureHandler", func(t *testing.T) {
		r := setupRunner(t, withOnFailure(successStep("onFailure")))

		plan := r.newPlan(t, failStep("1"))

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "onFailure", core.NodeSucceeded)
	})
	t.Run("CancelOnSignal", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand("sleep 0.5")),
		)

		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			plan.signal(syscall.SIGTERM)
		}()

		result := plan.assertRun(t, core.Aborted)

		result.assertNodeStatus(t, "1", core.NodeAborted)
	})
	t.Run("Repeat", func(t *testing.T) {
		r := setupRunner(t)
		repeatMarker := filepath.Join(t.TempDir(), "repeat.marker")

		plan := r.newPlan(t,
			newStep("1",
				withScript(func() string {
					if windowsShellTest() {
						return fmt.Sprintf(`
								if (-not (Test-Path %s)) {
									%s
									exit 0
								}
								%s
							`, test.PowerShellQuote(repeatMarker), createEmptyFileCommand(repeatMarker), test.Sleep(5*time.Second))
					}
					return fmt.Sprintf(`
							if [ ! -f %s ]; then
								%s
								exit 0
							fi
							%s
						`, test.PosixQuote(repeatMarker), createEmptyFileCommand(repeatMarker), test.Sleep(5*time.Second))
				}()),
				withRepeatPolicy(true, time.Millisecond*100),
			),
		)

		go func() {
			waitForNodeDoneCount(plan.Plan, "1", 1, 5*time.Second)
			plan.cancel(t)
		}()

		result := plan.assertRun(t, core.Aborted)

		// 1 should be repeated 2 times
		result.assertNodeStatus(t, "1", core.NodeAborted)

		node := result.nodeByName(t, "1")
		// Windows can report the cancellation before the first repeat is committed,
		// but it must never count a completed second execution.
		if windowsShellTest() {
			require.LessOrEqual(t, node.State().DoneCount, 1)
		} else {
			require.Equal(t, 1, node.State().DoneCount)
		}
	})
	t.Run("RepeatFail", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1",
				withCommand("exit 1"),
				withRepeatPolicy(true, time.Millisecond*50),
			),
		)

		result := plan.assertRun(t, core.Failed)

		// Done count should be 1 because it failed and not repeated
		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")
		require.Equal(t, 1, node.State().DoneCount)
	})
	t.Run("StopRepetitiveTaskGracefully", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1",
				withCommand("sleep 0.1"),
				withRepeatPolicy(true, time.Millisecond*50),
			),
		)

		done := make(chan struct{})
		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			plan.signal(syscall.SIGTERM)
			close(done)
		}()

		result := plan.assertRun(t, core.Succeeded)
		<-done

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
	})
	t.Run("WorkingDirNoExist", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withWorkingDir("/nonexistent"),
				withScript("echo 1"),
			),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeFailed)

		if windowsShellTest() {
			require.Contains(t, strings.ToLower(result.Error.Error()), "cannot find the path specified")
		} else {
			require.Contains(t, result.Error.Error(), "no such file or directory")
		}
	})
	t.Run("OutputVariables", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// 1: echo hello > OUT
		// 2: echo $OUT > RESULT
		plan := r.newPlan(t,
			newStep("1", withCommand("echo hello"), withOutput("OUT")),
			newStep("2", withCommand("echo $OUT"), withDepends("1"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)

		node := result.nodeByName(t, "2")

		// check if RESULT variable is set to "hello"
		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=hello", output, "expected output %q, got %q", "hello", output)
	})
	t.Run("OutputInheritance", func(t *testing.T) {
		r := setupRunner(t)

		// 1: echo hello > OUT
		// 2: echo world > OUT2 (depends on 1)
		// 3: echo $OUT $OUT2 > RESULT (depends on 2)
		// RESULT should be "hello world"
		plan := r.newPlan(t,
			newStep("1", withCommand("echo hello"), withOutput("OUT")),
			newStep("2", withCommand("echo world"), withOutput("OUT2"), withDepends("1")),
			newStep("3", withCommand("echo $OUT $OUT2"), withDepends("2"), withOutput("RESULT")),
			newStep("4", withCommand("sleep 0.1")),
			// 5 should not have reference to OUT or OUT2
			newStep("5", withCommand("echo $OUT $OUT2"), withDepends("4"), withOutput("RESULT2")),
		)

		result := plan.assertRun(t, core.Succeeded)

		node := result.nodeByName(t, "3")
		output, _ := node.NodeData().State.OutputVariables.Load("RESULT")
		outputText, ok := output.(string)
		require.True(t, ok, "output variable is not a string")
		normalizedOutput := "RESULT=" + strings.Join(strings.Fields(strings.TrimPrefix(outputText, "RESULT=")), " ")
		require.Equal(t, "RESULT=hello world", normalizedOutput, "expected output %q, got %q", "hello world", output)

		node2 := result.nodeByName(t, "5")
		output2, _ := node2.NodeData().State.OutputVariables.Load("RESULT2")
		require.Equal(t, "RESULT2=", output2, "expected output %q, got %q", "", output)
	})
	t.Run("OutputJSONReference", func(t *testing.T) {
		r := setupRunner(t)

		jsonData := `{"key": "value"}`
		plan := r.newPlan(t,
			newStep("1", withCommand(test.Output(jsonData)), withOutput("OUT")),
			newStep("2", withCommand(test.ExpandedOutput("${OUT.key}")), withDepends("1"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)

		// check if RESULT variable is set to "value"
		node := result.nodeByName(t, "2")

		output, _ := node.NodeData().State.OutputVariables.Load("RESULT")
		require.Equal(t, "RESULT=value", output, "expected output %q, got %q", "value", output)
	})
	t.Run("HandlingJSONWithSpecialChars", func(t *testing.T) {
		r := setupRunner(t)

		jsonData := "{\n\t\"key\": \"value\"\n}"
		plan := r.newPlan(t,
			newStep("1", withCommand(test.Output(jsonData)), withOutput("OUT")),
			newStep("2", withCommand(test.ExpandedOutput("${OUT.key}")), withDepends("1"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)

		// check if RESULT variable is set to "value"
		node := result.nodeByName(t, "2")

		output, _ := node.NodeData().State.OutputVariables.Load("RESULT")
		require.Equal(t, "RESULT=value", output, "expected output %q, got %q", "value", output)
	})
	t.Run("SpecialVarsDAGRUNLOGFILE", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand(test.ExpandedOutput("${DAG_RUN_LOG_FILE}")), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		outputRaw, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		output, ok := outputRaw.(string)
		require.True(t, ok, "output variable is not a string")
		require.True(t, strings.HasPrefix(output, "RESULT="), "unexpected output %q", output)
		require.True(t, strings.HasSuffix(strings.TrimPrefix(output, "RESULT="), ".log"), "unexpected output %q", output)
	})
	t.Run("SpecialVarsDAGRUNSTEPSTDOUTFILE", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand(test.ExpandedOutput("${DAG_RUN_STEP_STDOUT_FILE}")), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		outputRaw, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		output, ok := outputRaw.(string)
		require.True(t, ok, "output variable is not a string")
		require.True(t, strings.HasPrefix(output, "RESULT="), "unexpected output %q", output)
		require.True(t, strings.HasSuffix(strings.TrimPrefix(output, "RESULT="), ".out"), "unexpected output %q", output)
	})
	t.Run("SpecialVarsDAGRUNSTEPSTDERRFILE", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand(test.ExpandedOutput("${DAG_RUN_STEP_STDERR_FILE}")), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		outputRaw, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		output, ok := outputRaw.(string)
		require.True(t, ok, "output variable is not a string")
		require.True(t, strings.HasPrefix(output, "RESULT="), "unexpected output %q", output)
		require.True(t, strings.HasSuffix(strings.TrimPrefix(output, "RESULT="), ".err"), "unexpected output %q", output)
	})
	t.Run("SpecialVarsDAGRUNID", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand("echo $DAG_RUN_ID"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Regexp(t, `RESULT=[a-f0-9-]+`, output, "unexpected output %q", output)
	})
	t.Run("SpecialVarsDAGNAME", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand("echo $DAG_NAME"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=test_dag", output, "unexpected output %q", output)
	})
	t.Run("SpecialVarsDAGRUNSTEPNAME", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("step_test", withCommand("echo $DAG_RUN_STEP_NAME"), withOutput("RESULT")),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "step_test")

		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=step_test", output, "unexpected output %q", output)
	})
	t.Run("StdoutPathExpandsStepNameBeforePrepare", func(t *testing.T) {
		stdoutPath := filepath.Join(t.TempDir(), "dag_${DAG_RUN_STEP_NAME}_out.log")
		step := core.Step{Name: "second", Stdout: stdoutPath}
		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test_dag"}, "test-run", "test.log")
		ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

		err := node.Prepare(ctx, t.TempDir(), "test-run")
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, node.Teardown())
		})
		require.Equal(t, strings.ReplaceAll(stdoutPath, "${DAG_RUN_STEP_NAME}", "second"), node.Step().Stdout)
	})
	t.Run("StdoutPathExpandsStepEnvBeforePrepare", func(t *testing.T) {
		r := setupRunner(t)
		stdoutPath := filepath.Join(t.TempDir(), "${LOG_NAME}.log")

		plan := r.newPlan(t,
			newStep("writer",
				withCommand("echo meh"),
				withEnvVars("LOG_NAME=prepared-output"),
				withStdout(stdoutPath),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "writer")

		require.Equal(t, strings.ReplaceAll(stdoutPath, "${LOG_NAME}", "prepared-output"), node.Step().Stdout)
	})
	t.Run("StdoutPathExpandsUpstreamStepRefBeforePrepare", func(t *testing.T) {
		r := setupRunner(t)
		stdoutPath := "${first.stdout}.copy"

		plan := r.newPlan(t,
			newStep("first",
				withID("first"),
				withCommand("echo upstream"),
			),
			newStep("second",
				withDepends("first"),
				withCommand("echo downstream"),
				withStdout(stdoutPath),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		first := result.nodeByName(t, "first")
		second := result.nodeByName(t, "second")

		expected := first.GetStdout() + ".copy"
		require.Equal(t, expected, second.Step().Stdout)
		content, err := os.ReadFile(expected)
		require.NoError(t, err)
		assert.Contains(t, string(content), "downstream")
	})

	t.Run("DAGRunStatusNotAvailableToMainSteps", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1",
				withScript(dagRunStatusUnsetScript()),
				withOutput("RESULT"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		node := result.nodeByName(t, "1")

		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=unset", output, "unexpected output %q", output)
	})

	t.Run("RepeatPolicyRepeatsUntilCommandConditionMatchesExpected", func(t *testing.T) {
		r := setupRunner(t)

		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_test_%s.txt", uuid.Must(uuid.NewV7()).String()))
		stateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_state_%s.txt", uuid.Must(uuid.NewV7()).String()))
		t.Cleanup(func() {
			_ = os.Remove(counterFile)
			_ = os.Remove(stateFile)
		})
		require.NoError(t, os.WriteFile(stateFile, []byte("waiting"), 0600))
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = repeatExpectedCondition(stateFile, "ready")
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			_ = os.WriteFile(stateFile, []byte("ready"), 0600)
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	})

	t.Run("RepeatPolicyRepeatWhileConditionExits0", func(t *testing.T) {
		r := setupRunner(t)
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_exit0_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: repeatCounterEqualsCommand(counterFile, "1"),
					}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		node := result.nodeByName(t, "1")
		assert.Equal(t, 2, node.State().DoneCount)
	})

	t.Run("RepeatPolicyRepeatsWhileCommandExitCodeMatches", func(t *testing.T) {
		r := setupRunner(t)
		countFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_exitcode_%s", uuid.Must(uuid.NewV7()).String()))
		err := os.Remove(countFile)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		defer func() {
			err := os.Remove(countFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
		}()
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterExitCodeScript(countFile)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile
					step.RepeatPolicy.ExitCode = []int{42}
					step.RepeatPolicy.Interval = 50 * time.Millisecond
				},
			),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		node := result.nodeByName(t, "1")
		assert.GreaterOrEqual(t, node.State().DoneCount, 2)
	})

	t.Run("RepeatPolicyRepeatsUntilFileConditionMatchesExpected", func(t *testing.T) {
		r := setupRunner(t)
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_envvar_%s", uuid.Must(uuid.NewV7()).String()))
		stateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_envvar_state_%s", uuid.Must(uuid.NewV7()).String()))
		t.Cleanup(func() {
			if err := os.Remove(counterFile); err != nil && !os.IsNotExist(err) {
				t.Logf("cleanup: failed to remove %s: %v", counterFile, err)
			}
			if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
				t.Logf("cleanup: failed to remove %s: %v", stateFile, err)
			}
		})
		require.NoError(t, os.WriteFile(stateFile, []byte("pending"), 0600))
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = repeatExpectedCondition(stateFile, "done")
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			_ = os.WriteFile(stateFile, []byte("done"), 0600)
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		node := result.nodeByName(t, "1")
		assert.GreaterOrEqual(t, node.State().DoneCount, 2)
	})

	t.Run("RepeatPolicyRepeatsUntilOutputVarConditionMatchesExpected", func(t *testing.T) {
		r := setupRunner(t)
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_outputvar_%s", uuid.Must(uuid.NewV7()).String()))
		t.Cleanup(func() {
			err := os.Remove(counterFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
		})
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, true)),
				withOutput("OUT"),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: "$OUT",
						Expected:  "2",
					}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		node := result.nodeByName(t, "1")
		assert.Equal(t, 2, node.State().DoneCount)
	})
	t.Run("RetryPolicyWithOutputCapture", func(t *testing.T) {
		r := setupRunner(t)

		// Create a counter file for tracking retry attempts
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("retry_output_%s.txt", uuid.Must(uuid.NewV7()).String()))
		defer func() {
			_ = os.Remove(counterFile)
		}()

		// Step that outputs different values on each retry and fails until 3rd attempt
		plan := r.newPlan(t,
			newStep("1",
				withScript(retryOutputSequenceScript(counterFile, []string{
					"output_attempt_1",
					"output_attempt_2",
					"output_attempt_3_success",
				}, 3)),
				withOutput("RESULT"),
				withRetryPolicy(3, time.Millisecond*20),
			),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		require.Equal(t, 1, node.State().DoneCount)  // 1 successful execution
		require.Equal(t, 2, node.State().RetryCount) // 2 retries

		// Verify that output contains only the final successful attempt's output
		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=output_attempt_3_success", output, "expected final output, got %q", output)
	})
	t.Run("FailedStepWithOutputCapture", func(t *testing.T) {
		r := setupRunner(t)

		// Step that outputs data but fails
		plan := r.newPlan(t,
			newStep("1",
				withCommand("echo 'error_output'; exit 1"),
				withOutput("ERROR_MSG"),
			),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")

		// Verify that output is captured even on failure
		output, ok := node.NodeData().State.OutputVariables.Load("ERROR_MSG")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "ERROR_MSG=error_output", output, "expected output %q, got %q", "error_output", output)
	})
	t.Run("RetryPolicySubDAGRunWithOutputCapture", func(t *testing.T) {
		r := setupRunner(t)

		// Create a counter file for tracking retry attempts
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("retry_sub_output_%s.txt", uuid.Must(uuid.NewV7()).String()))
		defer func() {
			_ = os.Remove(counterFile)
		}()

		// Step that outputs different values on each retry and always fails
		plan := r.newPlan(t,
			newStep("1",
				withScript(retryOutputSequenceScript(counterFile, []string{
					"output_attempt_1",
					"output_attempt_2",
					"output_attempt_3",
				}, -1)),
				withOutput("RESULT"),
				withRetryPolicy(2, time.Millisecond*20),
			),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")
		require.Equal(t, 1, node.State().DoneCount)  // 1 execution (failed)
		require.Equal(t, 2, node.State().RetryCount) // 2 retries

		// Verify that output contains the final retry attempt's output
		output, ok := node.NodeData().State.OutputVariables.Load("RESULT")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "RESULT=output_attempt_3", output, "expected final output, got %q", output)
	})
}

// Step-level timeout tests
func TestRunner_StepLevelTimeout(t *testing.T) {
	t.Run("SingleStepTimeoutFailsStep", func(t *testing.T) {
		stepTimeout := platformTestDuration(100*time.Millisecond, 150*time.Millisecond)
		sleepDuration := platformTestDuration(200*time.Millisecond, 350*time.Millisecond)
		maxElapsed := platformTestDuration(1500*time.Millisecond, 3*time.Second)
		r := setupRunner(t, withTimeout(2*time.Second)) // large DAG timeout to ensure step-level fires first
		plan := r.newPlan(t,
			newStep("timeout_step",
				withCommand(test.Sleep(sleepDuration)), // longer than step timeout
				withStepTimeout(stepTimeout),
			),
			successStep("after", "timeout_step"),
		)

		start := time.Now()
		result := plan.assertRun(t, core.Failed)
		elapsed := time.Since(start)

		// Step should be aborted quickly (< 2s DAG timeout)
		assert.Less(t, elapsed, maxElapsed)
		result.assertNodeStatus(t, "timeout_step", core.NodeFailed)
		// Downstream dependency is aborted since runner cancels remaining steps after failure
		result.assertNodeStatus(t, "after", core.NodeAborted)

		node := result.nodeByName(t, "timeout_step")
		// Exit code should be 124 (standard timeout) and error message should mention timeout
		assert.Equal(t, 124, node.State().ExitCode)
		require.NotNil(t, node.State().Error)
		assert.Contains(t, node.State().Error.Error(), "step timed out")
	})

	t.Run("TimeoutPreemptsRetriesAndMarksFailed", func(t *testing.T) {
		stepTimeout := platformTestDuration(100*time.Millisecond, 150*time.Millisecond)
		sleepDuration := platformTestDuration(150*time.Millisecond, 300*time.Millisecond)
		r := setupRunner(t)
		plan := r.newPlan(t,
			newStep("retry_timeout",
				withCommand(test.JoinLines(
					test.Sleep(sleepDuration),
					"exit 1",
				)),
				withRetryPolicy(5, 50*time.Millisecond), // would retry many times if not timed out
				withStepTimeout(stepTimeout),            // shorter than sleep
			),
		)

		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "retry_timeout", core.NodeFailed)
		node := result.nodeByName(t, "retry_timeout")
		// Should not have retried because first attempt exceeded timeout
		assert.Equal(t, 0, node.State().RetryCount)
		assert.Equal(t, 124, node.State().ExitCode)
	})

	t.Run("ParallelStepsTimeoutFailIndividually", func(t *testing.T) {
		stepTimeout := platformTestDuration(80*time.Millisecond, 140*time.Millisecond)
		sleepDuration := platformTestDuration(200*time.Millisecond, 320*time.Millisecond)
		r := setupRunner(t, withMaxActiveRuns(3))
		plan := r.newPlan(t,
			newStep("p1", withCommand(test.Sleep(sleepDuration)), withStepTimeout(stepTimeout)),
			newStep("p2", withCommand(test.Sleep(sleepDuration)), withStepTimeout(stepTimeout)),
			newStep("p3", withCommand(test.Sleep(sleepDuration)), withStepTimeout(stepTimeout)),
		)

		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "p1", core.NodeFailed)
		result.assertNodeStatus(t, "p2", core.NodeFailed)
		result.assertNodeStatus(t, "p3", core.NodeFailed)
	})

	t.Run("StepLevelTimeoutOverridesLongDAGTimeoutAndFails", func(t *testing.T) {
		stepTimeout := platformTestDuration(120*time.Millisecond, 180*time.Millisecond)
		sleepDuration := platformTestDuration(300*time.Millisecond, 450*time.Millisecond)
		r := setupRunner(t, withTimeout(5*time.Second))
		plan := r.newPlan(t,
			newStep("short_timeout", withCommand(test.Sleep(sleepDuration)), withStepTimeout(stepTimeout)),
		)
		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "short_timeout", core.NodeFailed)
		node := result.nodeByName(t, "short_timeout")
		assert.Equal(t, 124, node.State().ExitCode)
	})
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status   core.Status
		expected string
	}{
		{core.NotStarted, "not_started"},
		{core.Running, "running"},
		{core.Failed, "failed"},
		{core.Aborted, "aborted"},
		{core.Succeeded, "succeeded"},
		{core.Queued, "queued"},
		{core.Status(999), "unknown"}, // Invalid status defaults to "unknown"
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestStatus_IsActive(t *testing.T) {
	tests := []struct {
		status   core.Status
		expected bool
	}{
		{core.NotStarted, false},
		{core.Running, true},
		{core.Failed, false},
		{core.Aborted, false},
		{core.Succeeded, false},
		{core.Queued, true},
	}

	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsActive())
		})
	}
}

func TestRunner_StatusPrecedence(t *testing.T) {
	t.Run("RejectedTakesPrecedenceOverWaiting", func(t *testing.T) {
		t.Parallel()

		dag := &core.DAG{Name: "precedence_test"}
		nodes := []*runtime.Node{
			runtime.NodeWithData(runtime.NodeData{
				Step:  core.Step{Name: "rejected_step"},
				State: runtime.NodeState{Status: core.NodeRejected},
			}),
			runtime.NodeWithData(runtime.NodeData{
				Step:  core.Step{Name: "waiting_step"},
				State: runtime.NodeState{Status: core.NodeWaiting},
			}),
		}

		plan, err := runtime.NewPlanFromNodes(nodes...)
		require.NoError(t, err)

		runner := runtime.New(&runtime.Config{})
		ctx := runtime.NewContext(context.Background(), dag, "test-run-id", "/tmp/test.log")

		status := runner.Status(ctx, plan)
		require.Equal(t, core.Rejected, status, "rejected should take precedence over waiting")
	})
}

func TestRunner_DryRun(t *testing.T) {
	r := setupRunner(t, func(cfg *runtime.Config) {
		cfg.Dry = true
	})

	plan := r.newPlan(t,
		successStep("1"),
		successStep("2", "1"),
		successStep("3", "2"),
	)

	result := plan.assertRun(t, core.Succeeded)

	// In dry run, steps should be marked as success without actual execution
	result.assertNodeStatus(t, "1", core.NodeSucceeded)
	result.assertNodeStatus(t, "2", core.NodeSucceeded)
	result.assertNodeStatus(t, "3", core.NodeSucceeded)
}

func TestRunner_DryRunWithHandlers(t *testing.T) {
	r := setupRunner(t,
		func(cfg *runtime.Config) {
			cfg.Dry = true
		},
		withOnExit(successStep("onExit")),
		withOnSuccess(successStep("onSuccess")),
	)

	plan := r.newPlan(t, successStep("1"))

	result := plan.assertRun(t, core.Succeeded)

	result.assertNodeStatus(t, "1", core.NodeSucceeded)
	result.assertNodeStatus(t, "onExit", core.NodeSucceeded)
	result.assertNodeStatus(t, "onSuccess", core.NodeSucceeded)
}

func TestRunner_ConcurrentExecution(t *testing.T) {
	steps := func() []core.Step {
		return []core.Step{
			newStep("1", withScript("sleep 0.3")),
			newStep("2", withScript("sleep 0.3")),
			newStep("3", withScript("sleep 0.3")),
		}
	}

	sequential := setupRunner(t, withMaxActiveRuns(1))
	planSequential := sequential.newPlan(t, steps()...)
	startSequential := time.Now()
	resultSequential := planSequential.assertRun(t, core.Succeeded)
	elapsedSequential := time.Since(startSequential)
	resultSequential.assertNodeStatus(t, "1", core.NodeSucceeded)
	resultSequential.assertNodeStatus(t, "2", core.NodeSucceeded)
	resultSequential.assertNodeStatus(t, "3", core.NodeSucceeded)

	concurrent := setupRunner(t, withMaxActiveRuns(3))
	planConcurrent := concurrent.newPlan(t, steps()...)
	startConcurrent := time.Now()
	resultConcurrent := planConcurrent.assertRun(t, core.Succeeded)
	elapsedConcurrent := time.Since(startConcurrent)
	resultConcurrent.assertNodeStatus(t, "1", core.NodeSucceeded)
	resultConcurrent.assertNodeStatus(t, "2", core.NodeSucceeded)
	resultConcurrent.assertNodeStatus(t, "3", core.NodeSucceeded)

	assert.Greater(t, elapsedSequential, elapsedConcurrent)
	assert.Greater(t, elapsedSequential-elapsedConcurrent, 200*time.Millisecond)
}

func TestRunner_ErrorHandling(t *testing.T) {
	t.Run("SetupError", func(t *testing.T) {
		// Create a runner with invalid log directory
		invalidLogDir := "/nonexistent/path/that/should/not/exist"
		if windowsShellTest() {
			invalidLogDir = filepath.Join(t.TempDir(), "invalid*logdir")
		}
		r := setupRunner(t, func(cfg *runtime.Config) {
			cfg.LogDir = invalidLogDir
		})

		plan := r.newPlan(t, successStep("1"))

		// Should fail during setup
		dag := &core.DAG{Name: "test_dag"}
		logFilename := fmt.Sprintf("%s_%s.log", dag.Name, r.cfg.DAGRunID)
		logFilePath := filepath.Join(r.cfg.LogDir, logFilename)

		ctx := runtime.NewContext(plan.Context, dag, r.cfg.DAGRunID, logFilePath)

		err := r.runner.Run(ctx, plan.Plan, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create log directory")
	})

	t.Run("PanicRecovery", func(t *testing.T) {
		r := setupRunner(t)

		// Exercise failed-step propagation without relying on shell-specific
		// process signaling behavior, which is slow on Windows runners.
		panicStep := newStep("panic", withScript(`
			`+test.Output("About to panic")+`
			exit 1
		`))

		plan := r.newPlan(t, panicStep)

		// The runner should recover from the panic and mark the step as error
		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "panic", core.NodeFailed)
	})
}

func TestRunner_Metrics(t *testing.T) {
	r := setupRunner(t)

	plan := r.newPlan(t,
		successStep("1"),
		failStep("2"),
		newStep("3", withPrecondition(&core.Condition{
			Condition: "exit 1",
		})),
		successStep("4", "1"),
	)

	result := plan.assertRun(t, core.Failed)

	// Get metrics
	metrics := r.runner.GetMetrics()

	assert.Equal(t, 4, metrics["totalNodes"])
	assert.Equal(t, 2, metrics["completedNodes"]) // 1 and 4
	assert.Equal(t, 1, metrics["failedNodes"])    // 2
	assert.Equal(t, 1, metrics["skippedNodes"])   // 3
	assert.Equal(t, 0, metrics["canceledNodes"])
	assert.NotEmpty(t, metrics["totalExecutionTime"])

	// Verify individual node statuses
	result.assertNodeStatus(t, "1", core.NodeSucceeded)
	result.assertNodeStatus(t, "2", core.NodeFailed)
	result.assertNodeStatus(t, "3", core.NodeSkipped)
	result.assertNodeStatus(t, "4", core.NodeSucceeded)
}

func TestRunner_DAGPreconditions(t *testing.T) {
	t.Run("DAGPreconditionNotMet", func(t *testing.T) {
		r := setupRunner(t)

		// Create DAG with precondition that will fail
		dag := &core.DAG{
			Name: "test_dag",
			Preconditions: []*core.Condition{
				{
					Condition: "exit 1", // This will fail
				},
			},
		}

		plan := r.newPlan(t, successStep("1"))

		// Custom schedule with DAG preconditions
		logFilename := fmt.Sprintf("%s_%s.log", dag.Name, r.cfg.DAGRunID)
		logFilePath := filepath.Join(r.cfg.LogDir, logFilename)

		ctx := runtime.NewContext(plan.Context, dag, r.cfg.DAGRunID, logFilePath)

		err := r.runner.Run(ctx, plan.Plan, nil)
		require.NoError(t, err) // No error, but dag should be canceled

		// Check that the runner was canceled
		assert.Equal(t, core.Aborted, r.runner.Status(ctx, plan.Plan))
	})
}

func TestRunner_StatusDefersForcedStatusUntilTerminal(t *testing.T) {
	t.Run("RunningStatusWinsBeforeForcedTerminalStatus", func(t *testing.T) {
		r := setupRunner(t, withForcedStatus(core.Failed))
		plan := r.newPlan(t, newStep("1", withCommand("sleep 0.2")))

		dag := &core.DAG{Name: "test_dag", WorkingDir: plan.workDir}
		logFilename := fmt.Sprintf("%s_%s.log", dag.Name, r.cfg.DAGRunID)
		logFilePath := filepath.Join(r.cfg.LogDir, logFilename)
		ctx := runtime.NewContext(plan.Context, dag, r.cfg.DAGRunID, logFilePath)

		done := make(chan error, 1)
		go func() {
			done <- r.runner.Run(ctx, plan.Plan, nil)
		}()

		require.Eventually(t, func() bool {
			return r.runner.Status(ctx, plan.Plan) == core.Running
		}, 2*time.Second, 10*time.Millisecond)

		require.NoError(t, <-done)
		require.Equal(t, core.Failed, r.runner.Status(ctx, plan.Plan))
	})
}

func TestRunner_SignalHandling(t *testing.T) {
	t.Run("SignalWithDoneChannel", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand("sleep 0.5")),
			successStep("2", "1"),
		)

		done := make(chan bool, 1)

		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			r.runner.Signal(r.Context, plan.Plan, syscall.SIGTERM, done, false)
		}()

		start := time.Now()
		result := plan.assertRun(t, core.Aborted)

		// Wait for signal completion
		select {
		case <-done:
			// Signal handling completed
		case <-time.After(1 * time.Second):
			t.Fatal("Signal handling did not complete in time")
		}

		elapsed := time.Since(start)
		assert.Less(t, elapsed, 2*time.Second, "Should cancel quickly")

		result.assertNodeStatus(t, "1", core.NodeAborted)
		result.assertNodeStatus(t, "2", core.NodeNotStarted)
	})

	t.Run("SignalWithOverride", func(t *testing.T) {
		r := setupRunner(t)

		plan := r.newPlan(t,
			newStep("1", withCommand("sleep 0.5")),
		)

		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			r.runner.Signal(r.Context, plan.Plan, syscall.SIGKILL, nil, true)
		}()

		result := plan.assertRun(t, core.Aborted)
		result.assertNodeStatus(t, "1", core.NodeAborted)
	})
}

func TestRunner_ComplexDependencyChains(t *testing.T) {
	t.Run("DiamondDependency", func(t *testing.T) {
		r := setupRunner(t)

		// Create diamond dependency: 1 -> 2,3 -> 4
		plan := r.newPlan(t,
			successStep("1"),
			successStep("2", "1"),
			successStep("3", "1"),
			successStep("4", "2", "3"),
		)

		result := plan.assertRun(t, core.Succeeded)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
		result.assertNodeStatus(t, "4", core.NodeSucceeded)
	})

	t.Run("ComplexFailurePropagation", func(t *testing.T) {
		r := setupRunner(t)

		// 1 -> 2 (fail) -> 4
		//   -> 3 -------->
		plan := r.newPlan(t,
			successStep("1"),
			failStep("2", "1"),
			successStep("3", "1"),
			successStep("4", "2", "3"),
		)

		result := plan.assertRun(t, core.Failed)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeFailed)
		result.assertNodeStatus(t, "3", core.NodeSucceeded)
		result.assertNodeStatus(t, "4", core.NodeAborted) // Canceled due to 2's failure
	})
}

func TestRunner_EdgeCases(t *testing.T) {
	t.Run("EmptyPlan", func(t *testing.T) {
		r := setupRunner(t)
		plan := r.newPlan(t) // No steps

		result := plan.assertRun(t, core.Succeeded)
		assert.NoError(t, result.Error)
	})

	t.Run("SingleNodePlan", func(t *testing.T) {
		r := setupRunner(t)
		plan := r.newPlan(t, successStep("single"))

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "single", core.NodeSucceeded)
	})

	t.Run("AllNodesFail", func(t *testing.T) {
		r := setupRunner(t)
		plan := r.newPlan(t,
			failStep("1"),
			failStep("2"),
			failStep("3"),
		)

		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "1", core.NodeFailed)
		result.assertNodeStatus(t, "2", core.NodeFailed)
		result.assertNodeStatus(t, "3", core.NodeFailed)
	})
}

func TestRunner_HandlerNodeAccess(t *testing.T) {
	exitStep := successStep("onExit")
	successHandlerStep := successStep("onSuccess")
	failureStep := successStep("onFailure")
	cancelStep := successStep("onAbort")

	r := setupRunner(t,
		withOnExit(exitStep),
		withOnSuccess(successHandlerStep),
		withOnFailure(failureStep),
		withOnAbort(cancelStep),
	)

	// Run a simple plan to trigger setup
	plan := r.newPlan(t, successStep("1"))
	_ = plan.assertRun(t, core.Succeeded)

	// Access handler nodes
	assert.NotNil(t, r.runner.HandlerNode(core.HandlerOnExit))
	assert.NotNil(t, r.runner.HandlerNode(core.HandlerOnSuccess))
	assert.NotNil(t, r.runner.HandlerNode(core.HandlerOnFailure))
	assert.NotNil(t, r.runner.HandlerNode(core.HandlerOnAbort))
	assert.Nil(t, r.runner.HandlerNode(core.HandlerType("unknown")))
}

func TestRunner_PreconditionWithError(t *testing.T) {
	r := setupRunner(t)

	// Create a step with a precondition that will error (not just return false)
	plan := r.newPlan(t,
		newStep("1",
			withPrecondition(&core.Condition{
				Condition: "exit 2", // Exit with non-zero code
			}),
			withCommand("echo should_not_run"),
		),
	)

	result := plan.assertRun(t, core.Succeeded)

	// The step should be skipped but no error should be set for condition not met
	result.assertNodeStatus(t, "1", core.NodeSkipped)
	// Conditions that exit with non-zero are just "not met", not errors
}

func TestRunner_MultipleHandlerExecution(t *testing.T) {
	recordHandler := func(name string) core.Step {
		return newStep(name, withScript(fmt.Sprintf(`echo "Handler %s executed"`, name)))
	}

	r := setupRunner(t,
		withOnExit(recordHandler("onExit")),
		withOnFailure(recordHandler("onFailure")),
	)

	plan := r.newPlan(t, failStep("1"))

	result := plan.assertRun(t, core.Failed)

	// Both onFailure and onExit should execute
	result.assertNodeStatus(t, "1", core.NodeFailed)
	result.assertNodeStatus(t, "onFailure", core.NodeSucceeded)
	result.assertNodeStatus(t, "onExit", core.NodeSucceeded)
}

func TestRunner_TimeoutDuringRetry(t *testing.T) {
	r := setupRunner(t, withTimeout(500*time.Millisecond))

	// Step that will keep retrying until timeout
	plan := r.newPlan(t,
		newStep("1",
			withCommand(test.JoinLines(
				test.Sleep(100*time.Millisecond),
				"exit 1",
			)),
			withRetryPolicy(10, 50*time.Millisecond), // Many retries
		),
	)

	start := time.Now()
	result := plan.assertRun(t, core.Failed)
	elapsed := time.Since(start)

	// Should timeout before completing all retries
	assert.Less(t, elapsed, 5*time.Second)
	result.assertNodeStatus(t, "1", core.NodeAborted)
}

func TestRunner_CancelDuringHandlerExecution(t *testing.T) {
	r := setupRunner(t,
		withOnExit(newStep("onExit", withScript(test.JoinLines(
			test.Output("handler started"),
			test.Sleep(100*time.Millisecond),
			test.Output("handler done"),
		)))),
	)

	plan := r.newPlan(t, successStep("1"))

	go func() {
		waitForHandlerNodeStatus(r.runner, core.HandlerOnExit, core.NodeRunning, 5*time.Second)
		r.runner.Cancel(plan.Plan)
	}()

	// Since we cancel during handler execution, the final status depends on timing
	// The plan completes successfully before cancel takes effect
	result := plan.assertRun(t, core.Succeeded)

	result.assertNodeStatus(t, "1", core.NodeSucceeded)
	// Handler should complete successfully
	result.assertNodeStatus(t, "onExit", core.NodeSucceeded)
}

func TestRunner_RepeatPolicyWithCancel(t *testing.T) {
	if windowsShellTest() {
		t.Skip("Skipping flaky shell-based repeat cancellation test on Windows")
	}

	r := setupRunner(t)
	counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_cancel_%s.txt", uuid.Must(uuid.NewV7()).String()))
	t.Cleanup(func() {
		_ = os.Remove(counterFile)
	})

	plan := r.newPlan(t,
		newStep("1",
			withScript(repeatCounterThenSleepScript(counterFile, 2, platformTestDuration(3*time.Second, 10*time.Second))),
			withRepeatPolicy(true, 20*time.Millisecond),
		),
	)

	cancelWait := platformTestDuration(5*time.Second, 30*time.Second)
	repeated := make(chan bool, 1)
	go func() {
		ready := waitForNodeRepeatScheduled(plan.Plan, "1", cancelWait)
		repeated <- ready
		if ready {
			time.Sleep(platformTestDuration(50*time.Millisecond, 1*time.Second))
		}
		r.runner.Cancel(plan.Plan)
	}()

	result := plan.assertRun(t, core.Aborted)
	result.assertNodeStatus(t, "1", core.NodeAborted)
	node := result.nodeByName(t, "1")
	assert.True(t, <-repeated, "runner should schedule repeat before cancel")
	assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	assert.Equal(t, 1, node.State().DoneCount)
}

func TestRunner_RepeatPolicyWithLimit(t *testing.T) {
	r := setupRunner(t)

	// Test repeat with limit
	plan := r.newPlan(t,
		newStep("1",
			withCommand(test.Output("repeat")),
			withRepeatPolicy(true, 100*time.Millisecond),
			func(step *core.Step) {
				step.RepeatPolicy.Limit = 3
			},
		),
	)

	result := plan.assertRun(t, core.Succeeded)
	result.assertNodeStatus(t, "1", core.NodeSucceeded)

	node := result.nodeByName(t, "1")
	// Should have executed exactly 3 times (initial + 2 repeats)
	assert.Equal(t, 3, node.State().DoneCount)
}

func TestRunner_RepeatPolicyWithLimitAndCondition(t *testing.T) {
	r := setupRunner(t)

	counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_limit_%s", uuid.Must(uuid.NewV7()).String()))
	defer func() { _ = os.Remove(counterFile) }()

	// Test repeat with limit and condition
	plan := r.newPlan(t,
		newStep("1",
			withScript(fmt.Sprintf(`
					%s
					echo "PENDING"
				`, repeatCounterScript(counterFile, false))),
			func(step *core.Step) {
				step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
				step.RepeatPolicy.Limit = 5
				step.RepeatPolicy.Condition = repeatExpectedCondition(counterFile, "10") // Would repeat forever but limit stops at 5
			},
		),
	)

	result := plan.assertRun(t, core.Succeeded)
	result.assertNodeStatus(t, "1", core.NodeSucceeded)

	node := result.nodeByName(t, "1")
	// Should have executed exactly 5 times due to limit
	assert.Equal(t, 5, node.State().DoneCount)

	// Verify counter file shows 5
	content, err := os.ReadFile(counterFile)
	assert.NoError(t, err)
	assert.Equal(t, "5", string(content))
}

func TestRunner_ComplexRetryScenarios(t *testing.T) {
	t.Run("RetryWithSignalTermination", func(t *testing.T) {
		r := setupRunner(t)

		// Create a script that will be terminated by signal
		plan := r.newPlan(t,
			newStep("1",
				withScript(`
					trap 'exit 143' TERM
					sleep 10
				`),
				withRetryPolicy(2, 20*time.Millisecond),
			),
		)

		go func() {
			waitForNodeStatus(plan.Plan, "1", core.NodeRunning, 5*time.Second)
			plan.signal(syscall.SIGTERM)
		}()

		result := plan.assertRun(t, core.Aborted)
		result.assertNodeStatus(t, "1", core.NodeAborted)
	})

	t.Run("RetryWithSpecificExitCodes", func(t *testing.T) {
		r := setupRunner(t)

		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("retry_codes_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()

		// Step that returns different exit codes
		plan := r.newPlan(t,
			newStep("1",
				withScript(retrySpecificExitCodeScript(counterFile)),
				withRetryPolicy(3, 20*time.Millisecond),
				func(step *core.Step) {
					step.RetryPolicy.ExitCodes = []int{42} // Only retry on exit code 42
				},
			),
		)

		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")
		// Should retry once (first failure with code 42, then fail with code 100)
		assert.Equal(t, 1, node.State().RetryCount)
	})

	// Test cases for behaviors when neither condition nor exitCode are present
	t.Run("RepeatPolicyBooleanTrueRepeatsWhileStepSucceeds", func(t *testing.T) {
		r := setupRunner(t)

		// Test repeat: true (boolean mode) - should repeat while step succeeds (no condition/exitCode)
		plan := r.newPlan(t,
			newStep("1",
				withCommand("echo boolean true mode"),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile // This is what repeat: true becomes
					step.RepeatPolicy.Interval = 20 * time.Millisecond
					step.RepeatPolicy.Limit = 3 // Limit to prevent infinite loop
					// No condition, no exitCode - should repeat while step succeeds
				},
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		// Should have executed exactly 3 times (limit reached, step always succeeds)
		assert.Equal(t, 3, node.State().DoneCount)
	})

	t.Run("RepeatPolicyBooleanTrueWithFailureStopsOnFailure", func(t *testing.T) {
		r := setupRunner(t)

		// Test repeat: true (boolean mode) with step that eventually fails
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_bool_fail_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()

		plan := r.newPlan(t,
			newStep("1",
				withScript(counterThresholdExitScript(counterFile, 2, 0, 1)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile // Boolean true mode
					step.RepeatPolicy.Interval = 20 * time.Millisecond
					// No condition, no exitCode - should stop when step fails
				},
			),
		)

		result := plan.assertRun(t, core.Failed)
		result.assertNodeStatus(t, "1", core.NodeFailed)

		node := result.nodeByName(t, "1")
		// Should have executed exactly 3 times (2 successes, then 1 failure stops it)
		assert.Equal(t, 3, node.State().DoneCount)
	})

	t.Run("RepeatPolicyUntilModeWithoutConditionRepeatsOnFailure", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit until mode without condition/exitCode (repeats until step succeeds)
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_none_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()

		plan := r.newPlan(t,
			newStep("1",
				withScript(counterThresholdExitScript(counterFile, 2, 1, 0)),
				withContinueOn(core.ContinueOn{
					ExitCode:    []int{1},
					Failure:     true,
					MarkSuccess: true,
				}),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Interval = 20 * time.Millisecond
					// No condition, no exitCode - should repeat until step succeeds
				},
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		// Should have executed exactly 3 times (fails twice, then succeeds)
		assert.Equal(t, 3, node.State().DoneCount)
	})

	t.Run("RepeatPolicyWhileWithConditionRepeatsWhileConditionSucceeds", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit while mode with condition
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_while_cond_counter_%s", uuid.Must(uuid.NewV7()).String()))
		gateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_while_cond_gate_%s", uuid.Must(uuid.NewV7()).String()))
		err := os.Remove(counterFile)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		err = os.Remove(gateFile)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		defer func() {
			err := os.Remove(counterFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
			err = os.Remove(gateFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
		}()
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: fileMissingCommand(gateFile),
					}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			f, _ := os.Create(gateFile)
			_ = f.Close()
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	})

	t.Run("RepeatPolicyWhileWithConditionAndExpectedRepeatsWhileMatches", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit while mode with condition and expected value
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_while_exp_counter_%s", uuid.Must(uuid.NewV7()).String()))
		stateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_while_exp_state_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() {
			_ = os.Remove(counterFile)
			_ = os.Remove(stateFile)
		}()

		// Write initial value
		err := os.WriteFile(stateFile, []byte("continue"), 0600)
		require.NoError(t, err)

		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeWhile
					step.RepeatPolicy.Condition = repeatExpectedCondition(stateFile, "continue")
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			_ = os.WriteFile(stateFile, []byte("stop"), 0600)
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	})

	t.Run("RepeatPolicyUntilWithConditionRepeatsUntilConditionSucceeds", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit until mode with condition (no expected)
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_cond_counter_%s", uuid.Must(uuid.NewV7()).String()))
		gateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_cond_gate_%s", uuid.Must(uuid.NewV7()).String()))
		err := os.Remove(counterFile)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		err = os.Remove(gateFile)
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		defer func() {
			err := os.Remove(counterFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
			err = os.Remove(gateFile)
			if err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
		}()
		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: fileExistsCommand(gateFile),
					}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			f, _ := os.Create(gateFile)
			_ = f.Close()
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	})

	t.Run("RepeatPolicyUntilWithConditionAndExpectedRepeatsUntilMatches", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit until mode with condition and expected value
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_exp_counter_%s", uuid.Must(uuid.NewV7()).String()))
		stateFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_exp_state_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() {
			_ = os.Remove(counterFile)
			_ = os.Remove(stateFile)
		}()

		// Write initial value
		err := os.WriteFile(stateFile, []byte("waiting"), 0600)
		require.NoError(t, err)

		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, false)),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = repeatExpectedCondition(stateFile, "ready")
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		go func() {
			if !waitForNodeRepeatScheduled(plan.Plan, "1", repeatConditionMutationTimeout()) {
				return
			}
			_ = os.WriteFile(stateFile, []byte("ready"), 0600)
		}()

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		assert.GreaterOrEqual(t, readRepeatCounterValue(t, counterFile), 2)
	})

	t.Run("RepeatPolicyUntilWithExitCodeRepeatsUntilExitCodeMatches", func(t *testing.T) {
		r := setupRunner(t)

		// Test explicit until mode with exit codes
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_until_exit_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()

		plan := r.newPlan(t,
			newStep("1",
				withScript(counterThresholdExitScript(counterFile, 2, 1, 42)),
				withContinueOn(core.ContinueOn{
					ExitCode:    []int{1},
					Failure:     true,
					MarkSuccess: true,
				}),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.ExitCode = []int{42}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		// Should have executed at least 3 times (until exit code 42)
		assert.GreaterOrEqual(t, node.State().DoneCount, 3)
	})

	t.Run("RepeatPolicyLimit", func(t *testing.T) {
		r := setupRunner(t)
		plan := r.newPlan(t,
			newStep("1",
				withCommand("echo limit"),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: "exit 1", // Will never be true
					}
					step.RepeatPolicy.Limit = 3
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		// Should have executed exactly 3 times (limit reached)
		assert.Equal(t, 3, node.State().DoneCount)
	})

	t.Run("RepeatPolicyOutputVariablesReloadedBeforeConditionEval", func(t *testing.T) {
		r := setupRunner(t)

		// Test that output variables are reloaded before evaluating repeat condition
		// Use a file-based counter to track iterations properly
		counterFile := filepath.Join(os.TempDir(), fmt.Sprintf("repeat_output_var_%s", uuid.Must(uuid.NewV7()).String()))
		defer func() { _ = os.Remove(counterFile) }()

		plan := r.newPlan(t,
			newStep("1",
				withScript(repeatCounterScript(counterFile, true)),
				withOutput("COUNTER"),
				func(step *core.Step) {
					step.RepeatPolicy.RepeatMode = core.RepeatModeUntil
					step.RepeatPolicy.Condition = &core.Condition{
						Condition: "$COUNTER",
						Expected:  "3",
					}
					step.RepeatPolicy.Interval = 20 * time.Millisecond
				},
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "1", core.NodeSucceeded)

		node := result.nodeByName(t, "1")
		// Should have executed exactly 3 times (until COUNTER equals 3)
		assert.Equal(t, 3, node.State().DoneCount)

		// Verify final output variable value
		output, ok := node.NodeData().State.OutputVariables.Load("COUNTER")
		require.True(t, ok, "output variable not found")
		require.Equal(t, "COUNTER=3", output)
	})
}

func TestRunner_StepIDVariableExpansion(t *testing.T) {
	r := setupRunner(t)

	// Test step ID usage in environment setup
	plan := r.newPlan(t,
		newStep("step1",
			withCommand("echo output1"),
			withOutput("OUT1"),
			func(step *core.Step) {
				step.ID = "s1"
			},
		),
		newStep("step2",
			withCommand("echo output2"),
			withOutput("OUT2"),
			func(step *core.Step) {
				step.ID = "s2"
			},
			withDepends("step1"),
		),
		newStep("step3",
			// This should have access to both step1 and step2 outputs via IDs
			withCommand(test.EnvOutputWithSeparator(" ", "OUT1", "OUT2")),
			withOutput("COMBINED"),
			withDepends("step2"),
		),
	)

	result := plan.assertRun(t, core.Succeeded)

	result.assertNodeStatus(t, "step1", core.NodeSucceeded)
	result.assertNodeStatus(t, "step2", core.NodeSucceeded)
	result.assertNodeStatus(t, "step3", core.NodeSucceeded)

	node := result.nodeByName(t, "step3")
	output, ok := node.NodeData().State.OutputVariables.Load("COMBINED")
	require.True(t, ok)
	assert.Equal(t, "COMBINED=output1 output2", output)
}

func TestRunner_UnexpectedFinalStatus(t *testing.T) {
	// This is a bit tricky to test as it requires the runner to be in an
	// unexpected state at the end. We'll simulate this by creating a custom
	// scenario that might trigger this edge case.
	r := setupRunner(t)

	// Create a plan with a step that might leave the runner in an unexpected state
	plan := r.newPlan(t,
		newStep("1", withCommand("echo test")),
	)

	// Schedule normally
	result := plan.assertRun(t, core.Succeeded)
	result.assertNodeStatus(t, "1", core.NodeSucceeded)

	// The warning log about unexpected final status would be logged internally
	// but we can't easily test for it without mock logging
}

func TestRunner_RetryPolicyDefaults(t *testing.T) {
	r := setupRunner(t)

	// Test retry with unhandled error type (not exec.ExitError)
	plan := r.newPlan(t,
		newStep("1",
			withScript(test.JoinLines(
				test.Stderr("Test error"),
				"exit 1",
			)),
			withRetryPolicy(1, 20*time.Millisecond),
		),
	)

	result := plan.assertRun(t, core.Failed)
	result.assertNodeStatus(t, "1", core.NodeFailed)

	node := result.nodeByName(t, "1")
	// Should have retried once
	assert.Equal(t, 1, node.State().RetryCount)
}

func TestRunner_StepRetryExecution(t *testing.T) {
	t.Run("RetrySuccessfulStep", func(t *testing.T) {
		r := setupRunner(t)

		// A -> B -> C, all successful
		dag := &core.DAG{
			Steps: []core.Step{
				{Name: "A", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"A"}}}},
				{Name: "B", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"B"}}}, Depends: []string{"A"}},
				{Name: "C", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"C"}}}, Depends: []string{"B"}},
			},
		}

		// Initial run - all successful
		plan := r.newPlan(t,
			successStep("A"),
			successStep("B", "A"),
			successStep("C", "B"),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "A", core.NodeSucceeded)
		result.assertNodeStatus(t, "B", core.NodeSucceeded)
		result.assertNodeStatus(t, "C", core.NodeSucceeded)

		// Create nodes with their current states
		nodes := []*runtime.Node{
			runtime.NodeWithData(runtime.NodeData{
				Step:  dag.Steps[0],
				State: runtime.NodeState{Status: core.NodeSucceeded},
			}),
			runtime.NodeWithData(runtime.NodeData{
				Step:  dag.Steps[1],
				State: runtime.NodeState{Status: core.NodeSucceeded},
			}),
			runtime.NodeWithData(runtime.NodeData{
				Step:  dag.Steps[2],
				State: runtime.NodeState{Status: core.NodeSucceeded},
			}),
		}

		// Retry step B
		retryPlan, err := runtime.CreateStepRetryPlan(dag, nodes, "B")
		require.NoError(t, err)

		// Schedule the retry
		retryResult := planHelper{testHelper: r, Plan: retryPlan}.assertRun(t, core.Succeeded)

		// A and C should remain unchanged, only B should be re-executed
		retryResult.assertNodeStatus(t, "A", core.NodeSucceeded)
		retryResult.assertNodeStatus(t, "B", core.NodeSucceeded)
		retryResult.assertNodeStatus(t, "C", core.NodeSucceeded)
	})
}

// TestRunner_StepIDAccess tests that step ID variables are expanded correctly
func TestRunner_StepIDAccess(t *testing.T) {
	t.Run("StepReferenceInCommand", func(t *testing.T) {
		r := setupRunner(t)

		// Create a DAG where step2 references step1's output
		plan := r.newPlan(t,
			newStep("step1",
				withID("first"),
				withCommand("echo 'output from step1'"),
				withOutput("STEP1_RESULT=success"),
			),
			newStep("step2",
				withID("second"),
				withDepends("step1"),
				withCommand("echo 'Step 1 stdout: ${first.stdout}'"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeSucceeded)

		// Step2 should have access to step1's stdout path
		node2 := result.nodeByName(t, "step2")
		stdoutFile := node2.GetStdout()
		stdoutContent, err := os.ReadFile(stdoutFile)
		require.NoError(t, err)
		assert.Contains(t, string(stdoutContent), "Step 1 stdout:")
	})
	t.Run("StepWithoutID", func(t *testing.T) {
		r := setupRunner(t)

		// Create a DAG where some steps don't have IDs
		plan := r.newPlan(t,
			newStep("step1",
				// No ID
				withCommand("echo 'no id'"),
			),
			newStep("step2",
				withID("with_id"),
				withCommand("echo 'has id'"),
				withOutput("VAR=value"),
			),
			newStep("step3",
				withID("third"),
				withDepends("step1", "step2"),
				// Can reference step2's stdout file path
				withCommand("echo 'Can reference: ${with_id.stdout}'"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeSucceeded)
		result.assertNodeStatus(t, "step3", core.NodeSucceeded)

		node3 := result.nodeByName(t, "step3")
		stdoutFile := node3.GetStdout()
		stdoutContent, err := os.ReadFile(stdoutFile)
		require.NoError(t, err)
		// Should contain the path to step2's stdout file
		assert.Contains(t, string(stdoutContent), "Can reference:")
		assert.Contains(t, string(stdoutContent), ".out")
	})

	t.Run("StepExitCodeReference", func(t *testing.T) {
		r := setupRunner(t)

		// Create a step that checks another step's exit code
		plan := r.newPlan(t,
			newStep("check",
				withID("checker"),
				withCommand("exit 42"),
				withContinueOn(core.ContinueOn{
					ExitCode: []int{42},
				}),
			),
			newStep("verify",
				withID("verifier"),
				withDepends("check"),
				withCommand("echo 'Checker exit code: ${checker.exit_code}'"),
			),
		)

		result := plan.assertRun(t, core.PartiallySucceeded)
		result.assertNodeStatus(t, "check", core.NodeFailed)
		result.assertNodeStatus(t, "verify", core.NodeSucceeded)

		nodeVerify := result.nodeByName(t, "verify")
		stdoutFile := nodeVerify.GetStdout()
		stdoutContent, err := os.ReadFile(stdoutFile)
		require.NoError(t, err)
		assert.Contains(t, string(stdoutContent), "Checker exit code: 42")
	})
}

// TestRunner_EventHandlerStepIDAccess tests that step ID references work in event handlers
func TestRunner_EventHandlerStepIDAccess(t *testing.T) {
	t.Run("OnSuccessHandlerWithStepReferences", func(t *testing.T) {
		r := setupRunner(t,
			withOnSuccess(newHandlerStep(t, "success_handler", "on_success",
				"echo 'Main output: ${main.stdout}, Worker result: ${worker.exit_code}'")),
		)

		plan := r.newPlan(t,
			newStep("main_step",
				withID("main"),
				withCommand("echo 'Main processing done'"),
			),
			newStep("worker_step",
				withID("worker"),
				withCommand(test.JoinLines(
					test.Output("Worker processing done"),
					"exit 0",
				)),
				withDepends("main_step"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)

		// All steps should succeed
		result.assertNodeStatus(t, "main_step", core.NodeSucceeded)
		result.assertNodeStatus(t, "worker_step", core.NodeSucceeded)

		// The handler should have executed
		result.assertNodeStatus(t, "success_handler", core.NodeSucceeded)

		// Get the handler node
		handlerNode := result.nodeByName(t, "success_handler")
		require.NotNil(t, handlerNode, "Success handler should have executed")

		// Check handler output contains references to main steps
		handlerOutput, err := os.ReadFile(handlerNode.GetStdout())
		require.NoError(t, err)
		output := string(handlerOutput)
		assert.Contains(t, output, "Main output:")
		assert.Contains(t, output, ".out") // Should contain stdout file path
		assert.Contains(t, output, "Worker result: 0")
	})

	t.Run("OnFailureHandlerWithStepReferences", func(t *testing.T) {
		r := setupRunner(t,
			withOnFailure(newHandlerStep(t, "failure_handler", "on_fail",
				"echo 'Failed step stderr: ${failing.stderr}, exit code: ${failing.exit_code}'")),
		)

		plan := r.newPlan(t,
			newStep("setup",
				withID("setup_step"),
				withCommand("echo 'Setup complete'"),
			),
			newStep("failing_step",
				withID("failing"),
				withCommand(test.JoinLines(
					test.Stderr("Error occurred"),
					"exit 1",
				)),
				withDepends("setup"),
			),
		)

		result := plan.assertRun(t, core.Failed)

		// Check step statuses
		result.assertNodeStatus(t, "setup", core.NodeSucceeded)
		result.assertNodeStatus(t, "failing_step", core.NodeFailed)

		// The failure handler should have executed
		result.assertNodeStatus(t, "failure_handler", core.NodeSucceeded)

		// Get the handler node
		handlerNode := result.nodeByName(t, "failure_handler")
		require.NotNil(t, handlerNode, "Failure handler should have executed")

		// Check handler output
		handlerOutput, err := os.ReadFile(handlerNode.GetStdout())
		require.NoError(t, err)
		output := string(handlerOutput)
		assert.Contains(t, output, "Failed step stderr:")
		assert.Contains(t, output, ".err") // Should contain stderr file path
		assert.Contains(t, output, "exit code: 1")
	})

	t.Run("OnExitHandlerWithMultipleStepReferences", func(t *testing.T) {
		r := setupRunner(t,
			withOnExit(newHandlerStep(t, "exit_handler", "on_exit",
				"echo 'Step1: ${step1.stdout}, Step2: ${step2.exit_code}, Step3: ${step3.stderr}'")),
		)

		plan := r.newPlan(t,
			newStep("first",
				withID("step1"),
				withCommand("echo 'First step output'"),
			),
			newStep("second",
				withID("step2"),
				withCommand("exit 0"),
				withDepends("first"),
			),
			newStep("third",
				withID("step3"),
				withCommand(test.Stderr("Warning message")),
				withDepends("second"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)

		// All main steps should succeed
		result.assertNodeStatus(t, "first", core.NodeSucceeded)
		result.assertNodeStatus(t, "second", core.NodeSucceeded)
		result.assertNodeStatus(t, "third", core.NodeSucceeded)

		// The exit handler should have executed
		result.assertNodeStatus(t, "exit_handler", core.NodeSucceeded)

		// Get the handler node
		handlerNode := result.nodeByName(t, "exit_handler")
		require.NotNil(t, handlerNode, "Exit handler should have executed")

		// Check handler output contains all step references
		handlerOutput, err := os.ReadFile(handlerNode.GetStdout())
		require.NoError(t, err)
		output := string(handlerOutput)
		assert.Contains(t, output, "Step1:")
		assert.Contains(t, output, ".out") // step1 stdout path
		assert.Contains(t, output, "Step2: 0")
		assert.Contains(t, output, "Step3:")
		assert.Contains(t, output, ".err") // step3 stderr path
	})

	t.Run("HandlerWithoutIDCannotBeReferenced", func(t *testing.T) {
		r := setupRunner(t,
			withOnExit(newHandlerStep(t, "exit_handler_no_id", "", "echo 'Handler executed'")),
		)

		plan := r.newPlan(t,
			newStep("main",
				withID("main_step"),
				withCommand("echo 'Main step'"),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "main", core.NodeSucceeded)

		// Handler should execute
		result.assertNodeStatus(t, "exit_handler_no_id", core.NodeSucceeded)

		// Get the handler node to verify it has no ID
		handlerNode := result.nodeByName(t, "exit_handler_no_id")
		assert.Empty(t, handlerNode.Step().ID, "Handler should have no ID")
	})

	t.Run("HandlersCanOnlyReferenceMainSteps", func(t *testing.T) {
		// Test that handlers can reference main steps but not other handlers
		// This is because handlers execute after all main steps are complete
		r := setupRunner(t,
			withOnSuccess(newHandlerStep(t, "first_handler", "handler1",
				"echo 'SUCCESS: Main returned ${main.exit_code}'")),
			withOnExit(newHandlerStep(t, "final_handler", "handler2",
				"echo 'FINAL: Main step output at ${main.stdout}, trying handler ref: ${handler1.stdout}'")),
		)

		plan := r.newPlan(t,
			newStep("main",
				withID("main"),
				withCommand(test.JoinLines(
					test.Output("Processing"),
					"exit 0",
				)),
			),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "main", core.NodeSucceeded)

		// Both handlers should have executed
		result.assertNodeStatus(t, "first_handler", core.NodeSucceeded)
		result.assertNodeStatus(t, "final_handler", core.NodeSucceeded)

		// Get the handler nodes
		successHandler := result.nodeByName(t, "first_handler")
		exitHandler := result.nodeByName(t, "final_handler")

		// Check success handler output
		successOutput, err := os.ReadFile(successHandler.GetStdout())
		require.NoError(t, err)
		assert.Contains(t, string(successOutput), "SUCCESS: Main returned 0")

		// Check exit handler output - it can reference main steps but not other handlers
		exitOutput, err := os.ReadFile(exitHandler.GetStdout())
		require.NoError(t, err)
		output := string(exitOutput)
		assert.Contains(t, output, "FINAL: Main step output at")
		assert.Contains(t, output, ".out") // Should contain main's stdout path
		// Handler reference should not be resolved
		assert.Contains(t, output, "${handler1.stdout}") // Should remain unresolved
	})
}

func TestRunner_DAGRunStatusHandlerEnv(t *testing.T) {
	r := setupRunner(t,
		withOnExit(newHandlerStep(t, "exit_handler", "", "echo status=${DAG_RUN_STATUS}")),
	)

	plan := r.newPlan(t, successStep("main"))
	result := plan.assertRun(t, core.Succeeded)

	handlerNode := result.nodeByName(t, "exit_handler")
	handlerOutput, err := os.ReadFile(handlerNode.GetStdout())
	require.NoError(t, err)

	assert.Equal(t, "status=succeeded", strings.TrimSpace(string(handlerOutput)))
}

func TestRunnerPartialSuccess(t *testing.T) {
	t.Run("NodeStatusPartialSuccess", func(t *testing.T) {
		r := setupRunner(t)

		// Create a plan where:
		// - step1 succeeds
		// - step2 fails but has continueOn.failure = true
		// - step3 depends on step2 and succeeds
		plan := r.newPlan(t,
			successStep("step1"),
			newStep("step2",
				withDepends("step1"),
				withCommand("false"), // This will fail
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
			successStep("step3", "step2"),
		)

		// The overall DAG should complete with partial success
		result := plan.assertRun(t, core.PartiallySucceeded)

		// Verify individual node statuses
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeFailed)
		result.assertNodeStatus(t, "step3", core.NodeSucceeded)
	})

	t.Run("NodeStatusPartialSuccessWithMarkSuccess", func(t *testing.T) {
		r := setupRunner(t)

		// Create a plan where:
		// - step1 succeeds
		// - step2 fails but has continueOn.failure = true and markSuccess = true
		// - step3 depends on step2 and succeeds
		plan := r.newPlan(t,
			successStep("step1"),
			newStep("step2",
				withDepends("step1"),
				withCommand("false"), // This will fail
				withContinueOn(core.ContinueOn{
					Failure:     true,
					MarkSuccess: true,
				}),
			),
			successStep("step3", "step2"),
		)

		// When markSuccess is true, the overall DAG should complete with success
		result := plan.assertRun(t, core.Succeeded)

		// Verify individual node statuses
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeSucceeded) // Marked as success
		result.assertNodeStatus(t, "step3", core.NodeSucceeded)
	})

	t.Run("MultipleFailuresWithContinueOn", func(t *testing.T) {
		r := setupRunner(t)

		// Create a plan where multiple steps fail but have continueOn
		plan := r.newPlan(t,
			newStep("step1",
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
			newStep("step2",
				withDepends("step1"),
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
			successStep("step3", "step2"),
		)

		// The overall DAG should complete with partial success
		result := plan.assertRun(t, core.PartiallySucceeded)

		// Verify individual node statuses
		result.assertNodeStatus(t, "step1", core.NodeFailed)
		result.assertNodeStatus(t, "step2", core.NodeFailed)
		result.assertNodeStatus(t, "step3", core.NodeSucceeded)
	})

	t.Run("NoSuccessfulStepsWithContinueOn", func(t *testing.T) {
		r := setupRunner(t)

		// Create a plan where all steps fail but have continueOn
		// This should still be an error, not partial success,
		// because partial success requires at least one successful step
		plan := r.newPlan(t,
			newStep("step1",
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
			newStep("step2",
				withDepends("step1"),
				withCommand("false"),
				withContinueOn(core.ContinueOn{
					Failure: true,
				}),
			),
		)

		// The overall DAG should complete with error since no steps succeeded
		result := plan.assertRun(t, core.Failed)

		// Verify individual node statuses
		result.assertNodeStatus(t, "step1", core.NodeFailed)
		result.assertNodeStatus(t, "step2", core.NodeFailed)
	})

	t.Run("FailureWithoutContinueOn", func(t *testing.T) {
		r := setupRunner(t)

		// Create a plan where a step fails without continueOn
		// This should result in an error status, not partial success
		plan := r.newPlan(t,
			successStep("step1"),
			failStep("step2", "step1"),    // This will fail without continueOn
			successStep("step3", "step1"), // This depends on step1, not step2
		)

		// The overall DAG should complete with error
		result := plan.assertRun(t, core.Failed)

		// Verify individual node statuses
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeFailed)
		result.assertNodeStatus(t, "step3", core.NodeSucceeded)
	})
}

func TestRunner_DeadlockDetection(t *testing.T) {
	t.Parallel()

	steps := []core.Step{
		{Name: "a"},
		{Name: "b", Depends: []string{"a"}},
	}

	plan, err := runtime.NewPlan(steps...)
	require.NoError(t, err)

	// Corrupt dependencies to create an unschedulable plan (self-dependency).
	for _, n := range plan.Nodes() {
		plan.DependencyMap[n.ID()] = []int{n.ID()}
	}

	cfg := &runtime.Config{
		LogDir:   t.TempDir(),
		DAGRunID: uuid.NewString(),
	}
	r := runtime.New(cfg)
	dag := &core.DAG{Name: "deadlock_dag"}
	logFile := filepath.Join(cfg.LogDir, dag.Name+".log")
	ctx := runtime.NewContext(context.Background(), dag, cfg.DAGRunID, logFile)

	progressCh := make(chan *runtime.Node, 2)
	err = r.Run(ctx, plan, progressCh)

	require.ErrorIs(t, err, runtime.ErrDeadlockDetected)
	require.Equal(t, core.Failed, r.Status(ctx, plan))
}

func TestNewEnvWithStepInfo(t *testing.T) {
	t.Parallel()

	plan, err := runtime.NewPlan(
		core.Step{ID: "s1", Name: "s1"},
		core.Step{ID: "s2", Name: "s2"},
		core.Step{Name: "no-id"},
	)
	require.NoError(t, err)

	env := runtime.NewPlanEnv(context.Background(), core.Step{Name: "current"}, plan)

	require.Len(t, env.StepMap, 2)
	require.Contains(t, env.StepMap, "s1")
	require.Contains(t, env.StepMap, "s2")
	require.Equal(t, "0", env.StepMap["s1"].ExitCode)
	require.Equal(t, "0", env.StepMap["s2"].ExitCode)
	require.NotContains(t, env.StepMap, "no-id")
}

func TestRunner_ChatMessagesHandler(t *testing.T) {
	t.Run("HandlerNotCalledForNonChatSteps", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		// Run non-chat steps
		plan := r.newPlan(t,
			successStep("step1"),
			successStep("step2", "step1"),
		)

		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
		result.assertNodeStatus(t, "step2", core.NodeSucceeded)

		// Handler should not have been called for writes since no chat steps
		assert.Equal(t, 0, handler.writeCalls)
	})

	t.Run("HandlerConfiguredCorrectly", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		// Verify handler is configured
		assert.NotNil(t, r.cfg.MessagesHandler)

		plan := r.newPlan(t, successStep("step1"))
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "step1", core.NodeSucceeded)
	})

	t.Run("SetupChatMessagesNoDependencies", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		// Chat step with no dependencies - should not read from handler
		plan := r.newPlan(t, chatStep("chat1"))
		// Step will fail (no LLM config), but setupChatMessages is called first
		_ = plan.assertRun(t, core.Failed)
	})

	t.Run("SetupChatMessagesWithDependencies", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		// Pre-populate handler with messages for dependency
		handler.messages["step1"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
			{Role: exec.RoleUser, Content: "hello"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		// First step succeeds, then chat step depends on it
		plan := r.newPlan(t,
			successStep("step1"),
			chatStep("chat1", "step1"),
		)
		// Chat step will fail (no LLM config), but messages should be read
		_ = plan.assertRun(t, core.Failed)

		// Messages were read from handler (verified by no panic/error)
	})

	t.Run("SetupChatMessagesReadError", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.readErr = fmt.Errorf("read error")

		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t,
			successStep("step1"),
			chatStep("chat1", "step1"),
		)
		// Should handle read error gracefully (logs warning, continues)
		_ = plan.assertRun(t, core.Failed)
	})

	t.Run("SetupChatMessagesDeduplicatesSystem", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		// Multiple system messages from different dependencies
		handler.messages["step1"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "first system"},
			{Role: exec.RoleUser, Content: "msg1"},
		}
		handler.messages["step2"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "second system"},
			{Role: exec.RoleUser, Content: "msg2"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t,
			successStep("step1"),
			successStep("step2"),
			chatStep("chat1", "step1", "step2"),
		)
		// Chat step will fail, but deduplication logic is exercised
		_ = plan.assertRun(t, core.Failed)
	})

	t.Run("SaveChatMessagesOnSuccess", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t, newStep("mock1", withExecutorType(chat.MockExecutorType)))
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "mock1", core.NodeSucceeded)

		assert.Equal(t, 1, handler.writeCalls)
		assert.NotEmpty(t, handler.messages["mock1"])
	})

	t.Run("SaveChatMessagesWriteError", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.writeErr = fmt.Errorf("write error")

		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t, newStep("mock1", withExecutorType(chat.MockExecutorType)))
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "mock1", core.NodeSucceeded)
	})

	t.Run("SaveChatMessagesWithInheritedContext", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.messages["step1"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t,
			successStep("step1"),
			newStep("mock1", withDepends("step1"), withExecutorType(chat.MockExecutorType)),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "mock1", core.NodeSucceeded)

		assert.Equal(t, 1, handler.writeCalls)
	})

	t.Run("SaveChatMessagesNoMessages", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t, newStep("empty1", withExecutorType(chat.MockEmptyExecutorType)))
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "empty1", core.NodeSucceeded)

		assert.Equal(t, 0, handler.writeCalls)
	})

	t.Run("AgentStepSavesMessages", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t, newStep("agent1", withExecutorType(agentstep.MockExecutorType)))
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "agent1", core.NodeSucceeded)

		assert.Equal(t, 1, handler.writeCalls)
		assert.NotEmpty(t, handler.messages["agent1"])

		// Verify cost metadata was preserved
		msgs := handler.messages["agent1"]
		var foundCost bool
		for _, m := range msgs {
			if m.Metadata != nil && m.Metadata.Cost > 0 {
				foundCost = true
				assert.Equal(t, "openai", m.Metadata.Provider)
				assert.Equal(t, "gpt-4", m.Metadata.Model)
				assert.InDelta(t, 0.001, m.Metadata.Cost, 1e-9)
			}
		}
		assert.True(t, foundCost, "expected at least one message with cost metadata")
	})

	t.Run("AgentStepInheritsFromDependency", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.messages["step1"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
			{Role: exec.RoleUser, Content: "prior message"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		plan := r.newPlan(t,
			successStep("step1"),
			newStep("agent1", withDepends("step1"), withExecutorType(agentstep.MockExecutorType)),
		)
		result := plan.assertRun(t, core.Succeeded)
		result.assertNodeStatus(t, "agent1", core.NodeSucceeded)

		assert.Equal(t, 1, handler.writeCalls)
		// The mock prepends inherited context, so saved messages should contain the inherited ones
		msgs := handler.messages["agent1"]
		assert.True(t, len(msgs) > 2, "expected inherited + own messages")
	})

	t.Run("HandlerNotCalledForAgentStepWithNoMessages", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		r := setupRunner(t, withMessagesHandler(handler))

		// agentStep helper creates a step with executor type "agent" (real executor),
		// which will fail since no agent config is available — but the gate should
		// allow the step through (setup/save calls won't panic).
		plan := r.newPlan(t, agentStep("agent_fail"))
		_ = plan.assertRun(t, core.Failed)

		// Handler must not be called: step failed so saveChatMessages is skipped.
		assert.Equal(t, 0, handler.writeCalls)
	})
}

func TestSetupPushBackConversation(t *testing.T) {
	t.Run("LoadsOwnMessagesForPushedBackAgentStep", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		// Pre-populate the step's own previous messages.
		handler.messages["agent1"] = []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
			{Role: exec.RoleUser, Content: "original prompt"},
			{Role: exec.RoleAssistant, Content: "previous response"},
		}
		// Also set dependency messages to verify they get replaced.
		handler.messages["dep1"] = []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "dep message"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		step := newStep("agent1",
			withExecutorType(core.ExecutorTypeAgent),
			withDepends("dep1"),
			withApproval(&core.ApprovalConfig{
				Prompt: "review this",
				Input:  []string{"FEEDBACK"},
			}),
		)

		plan := r.newPlan(t, successStep("dep1"), step)
		node := plan.GetNodeByName("agent1")
		require.NotNil(t, node)

		// Simulate push-back: set ApprovalIteration > 0
		node.SetApprovalIteration(1)

		ctx := context.Background()

		// First, setupChatMessages sets dependency messages.
		r.runner.SetupChatMessages(ctx, node)
		msgs := node.GetChatMessages()
		require.Len(t, msgs, 1)
		assert.Equal(t, "dep message", msgs[0].Content)

		// Then, setupPushBackConversation replaces with own messages.
		r.runner.SetupPushBackConversation(ctx, node)
		msgs = node.GetChatMessages()
		require.Len(t, msgs, 3)
		assert.Equal(t, "be helpful", msgs[0].Content)
		assert.Equal(t, "original prompt", msgs[1].Content)
		assert.Equal(t, "previous response", msgs[2].Content)
	})

	t.Run("NoOpForFirstExecution", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.messages["agent1"] = []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "should not load"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		step := newStep("agent1",
			withExecutorType(core.ExecutorTypeAgent),
			withApproval(&core.ApprovalConfig{}),
		)

		plan := r.newPlan(t, step)
		node := plan.GetNodeByName("agent1")
		// ApprovalIteration is 0 (default) — no push-back

		ctx := context.Background()
		r.runner.SetupPushBackConversation(ctx, node)

		// Should not load any messages.
		msgs := node.GetChatMessages()
		assert.Empty(t, msgs)
	})

	t.Run("NoOpForNonAgentStep", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.messages["cmd1"] = []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "should not load"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		step := newStep("cmd1",
			withCommand("echo hello"),
			withApproval(&core.ApprovalConfig{}),
		)

		plan := r.newPlan(t, step)
		node := plan.GetNodeByName("cmd1")
		node.SetApprovalIteration(1)

		ctx := context.Background()
		r.runner.SetupPushBackConversation(ctx, node)

		msgs := node.GetChatMessages()
		assert.Empty(t, msgs)
	})

	t.Run("NoOpWithoutApproval", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.messages["agent1"] = []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "should not load"},
		}

		r := setupRunner(t, withMessagesHandler(handler))

		step := newStep("agent1",
			withExecutorType(core.ExecutorTypeAgent),
		)

		plan := r.newPlan(t, step)
		node := plan.GetNodeByName("agent1")
		node.SetApprovalIteration(1)

		ctx := context.Background()
		r.runner.SetupPushBackConversation(ctx, node)

		msgs := node.GetChatMessages()
		assert.Empty(t, msgs)
	})

	t.Run("GracefulOnReadError", func(t *testing.T) {
		t.Parallel()

		handler := newMockMessagesHandler()
		handler.readErr = fmt.Errorf("storage error")

		r := setupRunner(t, withMessagesHandler(handler))

		step := newStep("agent1",
			withExecutorType(core.ExecutorTypeAgent),
			withApproval(&core.ApprovalConfig{}),
		)

		plan := r.newPlan(t, step)
		node := plan.GetNodeByName("agent1")
		node.SetApprovalIteration(1)

		ctx := context.Background()
		// Should not panic on read error.
		r.runner.SetupPushBackConversation(ctx, node)

		msgs := node.GetChatMessages()
		assert.Empty(t, msgs)
	})
}

func TestPushBackInputsExposeJSONHistoryEnv(t *testing.T) {
	t.Parallel()

	if windowsShellTest() {
		t.Skip("Skipping Unix-specific env assertion on Windows")
	}

	r := setupRunner(t)
	step := newStep("review",
		withScript("printf '%s\\n' \"$FEEDBACK\"\nprintf '%s' \"$DAG_PUSHBACK\""),
		withApproval(&core.ApprovalConfig{
			Input: []string{"FEEDBACK"},
		}),
	)

	plan := r.newPlan(t, step)
	node := plan.GetNodeByName("review")
	require.NotNil(t, node)

	node.SetApprovalIteration(1)
	node.SetPushBackInputs(map[string]string{"FEEDBACK": "needs more detail"})

	result := plan.assertRun(t, core.Waiting)
	result.assertNodeStatus(t, "review", core.NodeWaiting)

	output, err := os.ReadFile(result.nodeByName(t, "review").GetStdout())
	require.NoError(t, err)

	lines := strings.SplitN(strings.TrimSpace(string(output)), "\n", 2)
	require.Len(t, lines, 2)
	assert.Equal(t, "needs more detail", lines[0])

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &payload))

	assert.Equal(t, float64(1), payload["iteration"])

	inputs, ok := payload["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "needs more detail", inputs["FEEDBACK"])

	history, ok := payload["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)

	first, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), first["iteration"])

	historyInputs, ok := first["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "needs more detail", historyInputs["FEEDBACK"])
}

func TestPushBackInputsExposeJSONHistoryEnvForRewoundStep(t *testing.T) {
	t.Parallel()

	if windowsShellTest() {
		t.Skip("Skipping Unix-specific env assertion on Windows")
	}

	r := setupRunner(t)
	step := newStep("prepare",
		withScript("printf '%s\\n' \"$FEEDBACK\"\nprintf '%s' \"$DAG_PUSHBACK\""),
	)

	plan := r.newPlan(t, step)
	node := plan.GetNodeByName("prepare")
	require.NotNil(t, node)

	node.SetApprovalIteration(2)
	node.SetPushBackInputs(map[string]string{"FEEDBACK": "rerun from review"})
	node.SetPushBackHistory([]exec.PushBackEntry{
		{
			Iteration: 1,
			By:        "reviewer-a",
			At:        "2026-04-26T06:10:00Z",
			Inputs:    map[string]string{"FEEDBACK": "first pass"},
		},
		{
			Iteration: 2,
			By:        "reviewer-b",
			At:        "2026-04-26T06:20:00Z",
			Inputs:    map[string]string{"FEEDBACK": "rerun from review"},
		},
	})

	result := plan.assertRun(t, core.Succeeded)
	result.assertNodeStatus(t, "prepare", core.NodeSucceeded)

	output, err := os.ReadFile(result.nodeByName(t, "prepare").GetStdout())
	require.NoError(t, err)

	lines := strings.SplitN(strings.TrimSpace(string(output)), "\n", 2)
	require.Len(t, lines, 2)
	assert.Equal(t, "rerun from review", lines[0])

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &payload))

	assert.Equal(t, float64(2), payload["iteration"])
	assert.Equal(t, "reviewer-b", payload["by"])
	assert.Equal(t, "2026-04-26T06:20:00Z", payload["at"])

	inputs, ok := payload["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "rerun from review", inputs["FEEDBACK"])

	history, ok := payload["history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 2)
	second, ok := history[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "reviewer-b", second["by"])
	assert.Equal(t, "2026-04-26T06:20:00Z", second["at"])
}

func TestWaitStep(t *testing.T) {
	t.Run("WaitStepResultsInWaitStatus", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// 1 -> wait -> 3
		// When wait step completes, DAG should be in Wait status
		plan := r.newPlan(t,
			successStep("1"),
			waitStep("wait", "1"),
			successStep("3", "wait"),
		)

		result := plan.assertRun(t, core.Waiting)

		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "wait", core.NodeWaiting)
		result.assertNodeStatus(t, "3", core.NodeNotStarted)
	})

	t.Run("WaitStepBlocksDependentNodes", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// 1 -> wait -> 2 -> 3
		plan := r.newPlan(t,
			successStep("1"),
			waitStep("wait", "1"),
			successStep("2", "wait"),
			successStep("3", "2"),
		)

		result := plan.assertRun(t, core.Waiting)

		// Node 1 should succeed, wait should be waiting, 2 and 3 should not start
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "wait", core.NodeWaiting)
		result.assertNodeStatus(t, "2", core.NodeNotStarted)
		result.assertNodeStatus(t, "3", core.NodeNotStarted)
	})

	t.Run("ParallelBranchWithWaitStep", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// Two parallel branches: 1 -> 2 (normal), wait -> 3 (wait)
		plan := r.newPlan(t,
			successStep("1"),
			successStep("2", "1"),
			waitStep("wait"),
			successStep("3", "wait"),
		)

		result := plan.assertRun(t, core.Waiting)

		// Normal branch completes, wait branch blocks
		result.assertNodeStatus(t, "1", core.NodeSucceeded)
		result.assertNodeStatus(t, "2", core.NodeSucceeded)
		result.assertNodeStatus(t, "wait", core.NodeWaiting)
		result.assertNodeStatus(t, "3", core.NodeNotStarted)
	})

	t.Run("WaitStepAtStart", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// wait -> 1 -> 2
		plan := r.newPlan(t,
			waitStep("wait"),
			successStep("1", "wait"),
			successStep("2", "1"),
		)

		result := plan.assertRun(t, core.Waiting)

		result.assertNodeStatus(t, "wait", core.NodeWaiting)
		result.assertNodeStatus(t, "1", core.NodeNotStarted)
		result.assertNodeStatus(t, "2", core.NodeNotStarted)
	})

	t.Run("WaitStepWithInputConfig", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// Wait step with input configuration
		waitWithInputs := newStep("wait-inputs",
			withCommand("true"),
			withApproval(&core.ApprovalConfig{
				Prompt:   "Please provide approval",
				Input:    []string{"reason", "approver"},
				Required: []string{"reason"},
			}),
		)

		plan := r.newPlan(t,
			waitWithInputs,
			successStep("after", "wait-inputs"),
		)

		result := plan.assertRun(t, core.Waiting)

		result.assertNodeStatus(t, "wait-inputs", core.NodeWaiting)
		result.assertNodeStatus(t, "after", core.NodeNotStarted)
	})

	t.Run("MultipleWaitSteps", func(t *testing.T) {
		t.Parallel()
		r := setupRunner(t)

		// Multiple wait steps in sequence: wait1 -> wait2 -> final
		plan := r.newPlan(t,
			waitStep("wait1"),
			waitStep("wait2", "wait1"),
			successStep("final", "wait2"),
		)

		result := plan.assertRun(t, core.Waiting)

		// First wait should be waiting, others not started
		result.assertNodeStatus(t, "wait1", core.NodeWaiting)
		result.assertNodeStatus(t, "wait2", core.NodeNotStarted)
		result.assertNodeStatus(t, "final", core.NodeNotStarted)
	})
}
