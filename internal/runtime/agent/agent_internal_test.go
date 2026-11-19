// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/ssh"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailerConfigFromSMTP(t *testing.T) {
	t.Parallel()

	config, err := mailerConfigFromSMTP(&ir.SMTPConfig{
		Username: "sender@example.com",
		OAuth: &oauthconfig.Config{
			Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant",
			ClientID: "client", ClientSecret: "secret",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "smtp.office365.com", config.Host)
	assert.Equal(t, "587", config.Port)
	assert.Equal(t, "sender@example.com", config.Username)
	assert.NotNil(t, config.Token)
}

func TestErrorString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: "",
		},
		{
			name:     "SimpleError",
			err:      errors.New("test error"),
			expected: "test error",
		},
		{
			name:     "WrappedError",
			err:      errors.New("outer: inner error"),
			expected: "outer: inner error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errorString(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPanicToError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		panicObj    any
		expectedMsg string
	}{
		{
			name:        "WithError",
			panicObj:    errors.New("panic error"),
			expectedMsg: "panic error",
		},
		{
			name:        "WithString",
			panicObj:    "string panic",
			expectedMsg: "panic: string panic",
		},
		{
			name:        "WithInt",
			panicObj:    42,
			expectedMsg: "panic: 42",
		},
		{
			name:        "WithNil",
			panicObj:    nil,
			expectedMsg: "panic: <nil>",
		},
		{
			name:        "WithStruct",
			panicObj:    struct{ msg string }{msg: "test"},
			expectedMsg: "panic: {test}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := panicToError(tt.panicObj)
			assert.Equal(t, tt.expectedMsg, result.Error())
		})
	}
}

// DAG-level ssh fields resolve Dagu-owned references with the steps[].with
// rules, alongside unqualified environment syntax such as ${fqdn}. An
// unresolved reference stays literal.
func TestEvalSSHConfig(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
name: ssh-config
consts:
  - user: deploy
params:
  - name: fqdn
    type: string
steps:
  - name: ok
    run: "true"
`), spec.WithParams("fqdn=node.internal"))
	require.NoError(t, err)
	ctx := runtime.NewContext(context.Background(), dag, "test-run",
		filepath.Join(t.TempDir(), "run.log"),
		runtime.WithParams([]string{"fqdn=node.internal"}),
	)
	vars := runtime.GetEnv(ctx).UserEnvsMap()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "Param", raw: "${params.fqdn}", want: "node.internal"},
		{name: "EnvShorthand", raw: "/keys/${fqdn}/id_rsa", want: "/keys/node.internal/id_rsa"},
		{name: "Const", raw: "${consts.user}", want: "deploy"},
		{name: "BuiltinContext", raw: "${context.dag.name}", want: "ssh-config"},
		{name: "UnknownParam", raw: "${params.missing}", want: "${params.missing}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := evalSSHConfig(ctx, ssh.Config{
				Host:    tt.raw,
				Bastion: &ssh.BastionConfig{Host: tt.raw},
			}, vars)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Host)
			require.NotNil(t, got.Bastion)
			assert.Equal(t, tt.want, got.Bastion.Host)
		})
	}
}

// A run reads the dates its run recorded, so a retry needs neither the
// calendar nor the clock that resolved them.
func TestResolveDatesReusesRecordedDates(t *testing.T) {
	t.Parallel()

	recorded := []ir.DateValue{{Name: "last_open_day", Value: "2026-11-06"}}
	a := &Agent{
		dag: &ir.DAG{
			Calendar: "missing",
			Dates:    []ir.DateSpec{{Name: "last_open_day", Shift: []string{"-1bd"}}},
		},
		retryTarget: &ir.DAGRunStatus{ScheduleTime: "2026-11-09T02:00:00Z", Dates: recorded},
	}

	require.NoError(t, a.resolveDates(context.Background()))
	assert.Equal(t, recorded, a.dates)
}

// A run started without dates resolves them from its slot as it starts.
func TestResolveDatesAtStart(t *testing.T) {
	t.Parallel()

	a := &Agent{
		dag:            &ir.DAG{Dates: []ir.DateSpec{{Name: "target_day", Shift: []string{"-1d"}}}},
		scheduleTime:   "2026-11-10T02:00:00Z",
		scheduleWindow: ir.ScheduleWindow{Timezone: "UTC"},
	}

	require.NoError(t, a.resolveDates(context.Background()))
	assert.Equal(t, []ir.DateValue{{Name: "target_day", Value: "2026-11-09"}}, a.dates)
}

// A param whose eval reads the run's dates or context gets its value once
// the dates resolve, and so does the env that reads it.
func TestEvaluateDeferredParams(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
dates:
  target_day: -1d
params:
  - name: day
    eval: "${dates.target_day}"
  - name: run
    eval: "${context.run.id}"
env:
  - DAY: ${params.day}
steps:
  - run: echo ${params.day}
`))
	require.NoError(t, err)
	a := &Agent{
		dag:            dag,
		dagRunID:       "run-1",
		scheduleTime:   "2026-11-10T02:00:00Z",
		scheduleWindow: ir.ScheduleWindow{Timezone: "UTC"},
	}

	require.NoError(t, a.resolveDates(context.Background()))
	require.NoError(t, a.evaluateDeferredParams(context.Background()))
	assert.Equal(t, []string{"day=2026-11-09", "run=run-1"}, a.dag.Params)
	assert.Contains(t, a.dag.Env, "DAY=2026-11-09")
}

// A queued run of a DAG with dates that names no slot, such as a webhook
// run, takes the default slot of its creation, not of its start.
func TestResolveDatesTakesDefaultSlot(t *testing.T) {
	t.Parallel()

	monthly, err := ir.NewCronSchedule("CRON_TZ=UTC 0 2 1 * *")
	require.NoError(t, err)
	created := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	a := &Agent{
		dag: &ir.DAG{
			Schedule: []ir.Schedule{monthly},
			Dates:    []ir.DateSpec{{Name: "target_day", Shift: []string{"-1d"}}},
		},
		retryTarget: &ir.DAGRunStatus{CreatedAt: created.UnixMilli()},
	}

	require.NoError(t, a.resolveDates(context.Background()))
	assert.True(t, a.defaultSlot)
	slot, err := time.Parse(time.RFC3339, a.scheduleTime)
	require.NoError(t, err)
	assert.True(t, slot.Equal(time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)), a.scheduleTime)
	assert.Equal(t, []ir.DateValue{{Name: "target_day", Value: "2026-10-31"}}, a.dates)
}

// A sub-DAG of a parent with no slot takes the default slot of its own
// schedule, so business days follow that schedule's calendar, not the DAG's.
func TestResolveDatesOwnDefaultSlotCalendar(t *testing.T) {
	t.Parallel()

	daily, err := ir.NewCronSchedule("CRON_TZ=UTC 0 2 * * *")
	require.NoError(t, err)
	daily.Calendar = ir.CalendarWeekdays
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	require.NoError(t, daily.BindCalendar(weekdays))
	monday := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	a := &Agent{
		dag: &ir.DAG{
			Schedule: []ir.Schedule{daily},
			Calendar: "missing",
			Dates:    []ir.DateSpec{{Name: "last_open_day", Shift: []string{"-1bd"}}},
		},
		parentDAGRun: ir.NewDAGRunRef("parent", "run-1"),
		retryTarget:  &ir.DAGRunStatus{CreatedAt: monday.UnixMilli()},
	}

	require.NoError(t, a.resolveDates(context.Background()))
	assert.True(t, a.defaultSlot)
	assert.Equal(t, []ir.DateValue{{Name: "last_open_day", Value: "2026-11-06"}}, a.dates)
}
