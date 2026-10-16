// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package notification

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	notificationmodel "github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeliverCommandExportsRunContextAndRemovesLogFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	stdoutPath := filepath.Join(dir, "build.stdout")
	require.NoError(t, os.WriteFile(logPath, []byte("dag failed\n"), 0o644))
	require.NoError(t, os.WriteFile(stdoutPath, []byte("step output\n"), 0o644))
	outPath := filepath.Join(dir, "captured.txt")

	handlerErr := filepath.Join(dir, "handler.stderr")
	require.NoError(t, os.WriteFile(handlerErr, []byte("handler output\n"), 0o644))

	svc := New(nil, nil, WithPublicURL("https://dagu.example.com"))
	err := svc.deliverTarget(context.Background(), notificationmodel.Target{
		Type: notificationmodel.ProviderCommand,
		Command: &notificationmodel.CommandTarget{
			Command: captureCommand(outPath),
		},
	}, []chatbridge.NotificationEvent{{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:     "daily-report",
			DAGRunID: "run-1",
			Status:   ir.Failed,
			Log:      logPath,
			Nodes: []*ir.Node{{
				Step:   ir.Step{Name: "build"},
				Stdout: stdoutPath,
			}},
			OnFailure: &ir.Node{
				Step:   ir.Step{Name: "notify"},
				Stderr: handlerErr,
			},
		},
		ObservedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)

	body, err := os.ReadFile(outPath)
	require.NoError(t, err)
	text := string(body)
	assert.Contains(t, text, "daily-report")
	assert.Contains(t, text, "failed")
	assert.Contains(t, text, "dag failed")
	assert.Contains(t, text, "step output")
	assert.Contains(t, text, "# DAG log")
	assert.Contains(t, text, "# stdout build")
	assert.Contains(t, text, "# stderr notify")
	assert.Contains(t, text, "handler output")

	executionLog := executionLogPath(text)
	require.NotEmpty(t, executionLog)
	_, statErr := os.Stat(executionLog)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestDeliverCommandQuotesInterpolatedError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	outPath := filepath.Join(dir, "out.txt")
	payload := interpolatedErrorPayload(marker)
	svc := New(nil, nil)
	err := svc.deliverTarget(context.Background(), notificationmodel.Target{
		Type: notificationmodel.ProviderCommand,
		Command: &notificationmodel.CommandTarget{
			Command: interpolatedErrorCommand(outPath),
		},
	}, []chatbridge.NotificationEvent{{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Error:  payload,
		},
	}})
	require.NoError(t, err)
	_, statErr := os.Stat(marker)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
	body, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "hello")
}

func TestDeliverCommandTruncatesExecutionLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	require.NoError(t, os.WriteFile(logPath, bytes.Repeat([]byte("x"), 200), 0o644))
	outPath := filepath.Join(dir, "captured.txt")
	svc := New(nil, nil)
	svc.notificationLogLimit = 64
	err := svc.deliverTarget(context.Background(), notificationmodel.Target{
		Type: notificationmodel.ProviderCommand,
		Command: &notificationmodel.CommandTarget{
			Command: captureCommand(outPath),
		},
	}, []chatbridge.NotificationEvent{{
		Type: eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{
			Name:   "daily-report",
			Status: ir.Failed,
			Log:    logPath,
		},
	}})
	require.NoError(t, err)
	body, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "# log truncated")
	assert.NotContains(t, string(body), strings.Repeat("x", 80))
}

func TestQuoteNotificationValueIsOneShellWord(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `"hello & calc"`, quoteCmdNotificationValue("hello & calc"))
	assert.Equal(t, `"a"^""b"`, quoteCmdNotificationValue(`a"b`))
	assert.Equal(t, `"100%%"`, quoteCmdNotificationValue("100%"))
	if runtime.GOOS == "windows" {
		return
	}
	payload := "hello'; touch /tmp/pwned; printf '"
	quoted := quotePOSIXNotificationValue(payload)
	// #nosec G204 -- the shell command is fixed and the only variable is the quoted fixture.
	out, err := exec.Command("/bin/sh", "-c", "printf %s "+quoted).Output()
	require.NoError(t, err)
	assert.Equal(t, payload, string(out))
}

func TestDeliverCommandIncludesStderrOnFailure(t *testing.T) {
	t.Parallel()

	svc := New(nil, nil)
	err := svc.deliverTarget(context.Background(), notificationmodel.Target{
		Type: notificationmodel.ProviderCommand,
		Command: &notificationmodel.CommandTarget{
			Command: failingCommand(),
		},
	}, []chatbridge.NotificationEvent{{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
	}})
	require.Error(t, err)
	assert.ErrorContains(t, err, "boom")
	assert.ErrorContains(t, err, "status 7")
}

func TestDeliverCommandTimesOut(t *testing.T) {
	t.Parallel()

	svc := New(nil, nil)
	svc.notificationCommandTimeout = 200 * time.Millisecond
	start := time.Now()
	err := svc.deliverTarget(context.Background(), notificationmodel.Target{
		Type: notificationmodel.ProviderCommand,
		Command: &notificationmodel.CommandTarget{
			Command: slowCommand(),
		},
	}, []chatbridge.NotificationEvent{{
		Type:   eventstore.TypeDAGRunFailed,
		Status: &ir.DAGRunStatus{Name: "daily-report", Status: ir.Failed},
	}})
	require.Error(t, err)
	assert.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func interpolatedErrorPayload(marker string) string {
	if runtime.GOOS == "windows" {
		return "hello & echo should-not-run> " + marker
	}
	return "hello'; touch " + marker + "; printf 'should-not-run"
}

func interpolatedErrorCommand(outPath string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`echo {{run.error}}>"%s"`, outPath)
	}
	return fmt.Sprintf("printf '%%s' {{run.error}} > %q", outPath)
}

func captureCommand(outPath string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`(echo {{dag.name}}& echo %%DAG_NAME%%& echo %%STATUS%%& echo %%EXECUTION_LOG%%)> "%s" & if not "%%EXECUTION_LOG%%"=="" type "%%EXECUTION_LOG%%" >> "%s"`, outPath, outPath)
	}
	return fmt.Sprintf("printf '%%s\\n' '{{dag.name}}' \"$DAG_NAME\" \"$STATUS\" \"$EXECUTION_LOG\" > %q && if [ -n \"$EXECUTION_LOG\" ]; then cat \"$EXECUTION_LOG\" >> %q; fi", outPath, outPath)
}

func executionLogPath(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "daily-report" || line == "failed" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "dagu-notification-log-") {
			return line
		}
	}
	return ""
}

func failingCommand() string {
	if runtime.GOOS == "windows" {
		return "echo boom 1>&2& exit /b 7"
	}
	return "echo boom >&2; exit 7"
}

func slowCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 6 127.0.0.1 >NUL"
	}
	return "sleep 5"
}
