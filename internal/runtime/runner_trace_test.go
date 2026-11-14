// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var traceParentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-01$`)

// The tests below replace the global tracer provider and propagator, so they
// must not run in parallel with other tests.

func TestStepTraceContext(t *testing.T) {
	t.Run("StepSpan", func(t *testing.T) {
		recorder, tracer := setupTestTracing(t)
		plan := setupRunner(t).newPlan(t,
			newStep("1",
				withCommand(traceEnvCommand()),
				withOutput("TRACE"),
			),
		)

		var root trace.Span
		plan.Context, root = tracer.Start(withRemoteTraceState(t, plan.Context, "vendor=value"), "DAG: test_dag")
		result := plan.assertRun(t, ir.Succeeded)
		root.End()

		traceParent, traceState := splitTraceOutput(t, result.nodeByName(t, "1"))
		match := traceParentPattern.FindStringSubmatch(traceParent)
		require.NotNil(t, match, "unexpected TRACEPARENT %q", traceParent)

		step := endedSpan(t, recorder, "Step: 1")
		assert.Equal(t, root.SpanContext().TraceID().String(), match[1])
		assert.Equal(t, step.SpanContext().SpanID().String(), match[2])
		assert.Equal(t, root.SpanContext().SpanID(), step.Parent().SpanID())
		assert.Equal(t, "vendor=value", traceState)
	})

	// A trace context that the step declares itself wins over the step span,
	// and the two names stay together: the step span's TRACESTATE is not added
	// to a TRACEPARENT of another trace.
	t.Run("StepEnvWins", func(t *testing.T) {
		_, tracer := setupTestTracing(t)
		const own = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		plan := setupRunner(t).newPlan(t,
			newStep("1",
				withEnvVars("TRACEPARENT="+own),
				withCommand(traceEnvCommand()),
				withOutput("TRACE"),
			),
		)

		var root trace.Span
		plan.Context, root = tracer.Start(withRemoteTraceState(t, plan.Context, "vendor=value"), "DAG: test_dag")
		result := plan.assertRun(t, ir.Succeeded)
		root.End()

		traceParent, traceState := splitTraceOutput(t, result.nodeByName(t, "1"))
		assert.Equal(t, own, traceParent)
		assert.Equal(t, unsetTrace(), traceState)
	})

	// A step-local TRACEPARENT or TRACESTATE replaces a root-scope value of
	// the other name with an empty value, because the root value belongs to
	// another trace context.
	t.Run("StepEnvClearsRootCounterpart", func(t *testing.T) {
		const own = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		const rootParent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
		for _, tc := range []struct {
			name      string
			root      string
			step      string
			wantTrace string
		}{
			{"StepParentRootState", "TRACESTATE=root=state", "TRACEPARENT=" + own, own + "|"},
			{"StepStateRootParent", "TRACEPARENT=" + rootParent, "TRACESTATE=own=state", "|own=state"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, tracer := setupTestTracing(t)
				th := setupRunner(t)
				th.Config.Core.BaseEnv = config.NewBaseEnv(append(th.Config.Core.BaseEnv.AsSlice(), tc.root))
				plan := th.newPlan(t,
					newStep("1",
						withEnvVars(tc.step),
						withCommand(traceEnvCommand()),
						withOutput("TRACE"),
					),
				)

				var root trace.Span
				plan.Context, root = tracer.Start(plan.Context, "DAG: test_dag")
				result := plan.assertRun(t, ir.Succeeded)
				root.End()

				traceParent, traceState := splitTraceOutput(t, result.nodeByName(t, "1"))
				assert.Equal(t, tc.wantTrace, traceParent+"|"+traceState)
			})
		}
	})

	t.Run("StepEnvClears", func(t *testing.T) {
		_, tracer := setupTestTracing(t)
		plan := setupRunner(t).newPlan(t,
			newStep("1",
				withEnvVars("TRACEPARENT="),
				withCommand(traceEnvCommand()),
				withOutput("TRACE"),
			),
		)

		var root trace.Span
		plan.Context, root = tracer.Start(plan.Context, "DAG: test_dag")
		result := plan.assertRun(t, ir.Succeeded)
		root.End()

		traceParent, traceState := splitTraceOutput(t, result.nodeByName(t, "1"))
		assert.Equal(t, "", traceParent)
		assert.Equal(t, unsetTrace(), traceState)
	})

	t.Run("NoSpan", func(t *testing.T) {
		plan := setupRunner(t).newPlan(t,
			newStep("1",
				withCommand(traceEnvCommand()),
				withOutput("TRACE"),
			),
		)

		result := plan.assertRun(t, ir.Succeeded)

		traceParent, traceState := splitTraceOutput(t, result.nodeByName(t, "1"))
		assert.Equal(t, unsetTrace(), traceParent)
		assert.Equal(t, unsetTrace(), traceState)
	})
}

// traceEnvCommand prints TRACEPARENT and TRACESTATE as "parent|state". The
// POSIX shell prints "unset" for a name that is not set. PowerShell on
// Windows prints an empty string for a name that is not set or empty.
func traceEnvCommand() string {
	if windowsShellTest() {
		return `Write-Output ($env:TRACEPARENT + '|' + $env:TRACESTATE)`
	}
	return `printf '%s|%s' "${TRACEPARENT-unset}" "${TRACESTATE-unset}"`
}

// unsetTrace is what traceEnvCommand prints for a name that is not set.
func unsetTrace() string {
	if windowsShellTest() {
		return ""
	}
	return "unset"
}

func setupTestTracing(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return recorder, provider.Tracer("test")
}

// withRemoteTraceState returns ctx with a remote parent span that carries the
// given trace state, as a caller of Dagu would pass it.
func withRemoteTraceState(t *testing.T, ctx context.Context, state string) context.Context {
	t.Helper()

	traceState, err := trace.ParseTraceState(state)
	require.NoError(t, err)
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c},
		SpanID:     trace.SpanID{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
		Remote:     true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, parent)
}

func splitTraceOutput(t *testing.T, node *runtime.Node) (string, string) {
	t.Helper()

	output, ok := node.OutputVariablesMap()["TRACE"]
	require.True(t, ok, "output variable TRACE not found")
	traceParent, traceState, ok := strings.Cut(output, "|")
	require.True(t, ok, "unexpected output %q", output)
	return traceParent, traceState
}

func endedSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	require.FailNow(t, "span not recorded", "span %q not found", name)
	return nil
}
