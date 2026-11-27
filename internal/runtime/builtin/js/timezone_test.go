// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package js

import (
	"context"
	"sync"
	"testing"
	"time"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ctxWithTZ(dagTZ string, stepTZ *string) context.Context {
	scope := cmnvalue.NewEnvScope(nil, true)
	if dagTZ != "" {
		scope = scope.WithEntries(map[string]string{"TZ": dagTZ}, cmnvalue.EnvSourceDAGEnv)
	}
	if stepTZ != nil {
		scope = scope.WithEntries(map[string]string{"TZ": *stepTZ}, cmnvalue.EnvSourceStepEnv)
	}
	return runtime.WithEnv(context.Background(), runtime.Env{Scope: scope})
}

func TestJS_Timezone(t *testing.T) {
	t.Parallel()

	// Fixed instant 2026-11-12T17:14:00Z = 2026-11-13 02:14:00 JST (+09:00)
	const instantScript = `
const d = new Date(Date.UTC(2026, 10, 12, 17, 14, 0));
return {
  year: d.getFullYear(),
  month: d.getMonth() + 1,
  date: d.getDate(),
  day: d.getDay(),
  hours: d.getHours(),
  minutes: d.getMinutes(),
  offset: d.getTimezoneOffset(),
  iso: d.toISOString(),
  str: d.toString(),
  dateStr: d.toDateString(),
  timeStr: d.toTimeString(),
  localeStr: d.toLocaleString(),
  isDate: d instanceof Date
}`

	t.Run("TokyoMidnightRollover", func(t *testing.T) {
		t.Parallel()
		got := run(t, ctxWithTZ("Asia/Tokyo", nil), instantScript, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `{
  "year": 2026,
  "month": 11,
  "date": 13,
  "day": 5,
  "hours": 2,
  "minutes": 14,
  "offset": -540,
  "iso": "2026-11-12T17:14:00.000Z",
  "str": "Fri Nov 13 2026 02:14:00 GMT+0900 (JST)",
  "dateStr": "Fri Nov 13 2026",
  "timeStr": "02:14:00 GMT+0900 (JST)",
  "localeStr": "11/13/2026, 02:14:00",
  "isDate": true
}`, got.stdout)
	})

	t.Run("StepOverridesDAGAndDST", func(t *testing.T) {
		t.Parallel()
		stepTZ := "America/New_York"
		// Check winter (EST = -05:00) and summer (EDT = -04:00) in the same script.
		script := `
const winter = new Date(Date.UTC(2026, 0, 15, 3, 0, 0));
const summer = new Date(Date.UTC(2026, 6, 15, 3, 0, 0));
return {
  winter: [winter.getDate(), winter.getHours(), winter.getTimezoneOffset()],
  summer: [summer.getDate(), summer.getHours(), summer.getTimezoneOffset()]
}`
		got := run(t, ctxWithTZ("Asia/Tokyo", &stepTZ), script, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `{
  "winter": [14, 22, 300],
  "summer": [14, 23, 240]
}`, got.stdout)
	})

	t.Run("HalfHourOffsetKathmandu", func(t *testing.T) {
		t.Parallel()
		got := run(t, ctxWithTZ("Asia/Kathmandu", nil), `
const d = new Date(Date.UTC(2026, 10, 12, 18, 30, 0));
return [d.getDate(), d.getHours(), d.getMinutes(), d.getTimezoneOffset()]`, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `[13, 0, 15, -345]`, got.stdout)
	})

	t.Run("EmptyTZMeansUTC", func(t *testing.T) {
		t.Parallel()
		empty := ""
		got := run(t, ctxWithTZ("Asia/Tokyo", &empty), `
const d = new Date(Date.UTC(2026, 10, 12, 17, 14, 0));
return [d.getDate(), d.getHours(), d.getTimezoneOffset()]`, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `[12, 17, 0]`, got.stdout)
	})

	t.Run("LeadingColonTZ", func(t *testing.T) {
		t.Parallel()
		got := run(t, ctxWithTZ(":Asia/Tokyo", nil), `
const d = new Date(Date.UTC(2026, 10, 12, 17, 14, 0));
return [d.getDate(), d.getHours(), d.getTimezoneOffset()]`, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `[13, 2, -540]`, got.stdout)
	})

	t.Run("LocalConstructorsAndParseAndSetters", func(t *testing.T) {
		t.Parallel()
		script := `
const constructed = new Date(2026, 10, 13, 2, 14, 30, 500);
const twoDigit = new Date(99, 0, 1);
const largeMs = new Date(1970, 0, 1, 0, 0, 0, 1e13);
const parsedLocal = new Date("2026-11-13T02:14:00");
const parsedYearOnly = new Date("2026");
const parsedYearMonth = new Date("2026-11");
const parsedDateOnly = new Date("2026-11-13");
const parsedZ = new Date("2026-11-12T17:14:00Z");
const fnStr = Date(123);
const mut = new Date(Date.UTC(2026, 10, 12, 17, 14, 0));
mut.setHours(25, 30, 15, 250);
const inv = new Date(NaN);
return {
  constructedISO: constructed.toISOString(),
  twoDigitYear: twoDigit.getFullYear(),
  largeMsISO: largeMs.toISOString(),
  parsedLocalISO: parsedLocal.toISOString(),
  parsedYearOnlyISO: parsedYearOnly.toISOString(),
  parsedYearMonthISO: parsedYearMonth.toISOString(),
  parsedDateOnlyISO: parsedDateOnly.toISOString(),
  parsedZHoursLocal: parsedZ.getHours(),
  fnHasJST: fnStr.includes("+0900"),
  mutISO: mut.toISOString(),
  mutDate: mut.getDate(),
  mutHours: mut.getHours(),
  invStr: inv.toString(),
  invNaN: Number.isNaN(inv.getHours())
}`
		got := run(t, ctxWithTZ("Asia/Tokyo", nil), script, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `{
  "constructedISO": "2026-11-12T17:14:30.500Z",
  "twoDigitYear": 1999,
  "largeMsISO": "2286-11-20T08:46:40.000Z",
  "parsedLocalISO": "2026-11-12T17:14:00.000Z",
  "parsedYearOnlyISO": "2026-01-01T00:00:00.000Z",
  "parsedYearMonthISO": "2026-11-01T00:00:00.000Z",
  "parsedDateOnlyISO": "2026-11-13T00:00:00.000Z",
  "parsedZHoursLocal": 2,
  "fnHasJST": true,
  "mutISO": "2026-11-13T16:30:15.250Z",
  "mutDate": 14,
  "mutHours": 1,
  "invStr": "Invalid Date",
  "invNaN": true
}`, got.stdout)
	})

	t.Run("POSIXFixedOffsetSign", func(t *testing.T) {
		t.Parallel()
		// POSIX TZ=UTC+9 means 9h west of UTC (+540 JS offset); TZ=UTC-9 means 9h east (-540).
		west := run(t, ctxWithTZ("UTC+9", nil), `return new Date(Date.UTC(2026, 10, 12, 12, 0, 0)).getTimezoneOffset()`, nil)
		require.NoError(t, west.err)
		assert.Equal(t, "540\n", west.stdout)

		east := run(t, ctxWithTZ("UTC-9", nil), `return new Date(Date.UTC(2026, 10, 12, 12, 0, 0)).getTimezoneOffset()`, nil)
		require.NoError(t, east.err)
		assert.Equal(t, "-540\n", east.stdout)
	})

	t.Run("ConcurrentStepsStayIsolated", func(t *testing.T) {
		t.Parallel()
		zones := []struct {
			tz     string
			offset string
		}{
			{"Asia/Tokyo", "-540\n"},
			{"America/New_York", "300\n"},
			{"Asia/Kathmandu", "-345\n"},
			{"UTC", "0\n"},
		}
		var wg sync.WaitGroup
		for range 8 {
			for _, z := range zones {
				wg.Go(func() {
					got := run(t, ctxWithTZ(z.tz, nil), `return new Date(Date.UTC(2026, 0, 15, 12, 0, 0)).getTimezoneOffset()`, nil)
					require.NoError(t, got.err)
					assert.Equal(t, z.offset, got.stdout)
				})
			}
		}
		wg.Wait()
	})
}

func TestJS_ProcessTZFallback(t *testing.T) {
	origLocal := time.Local
	t.Setenv("TZ", "Asia/Tokyo")
	ctx := runtime.WithEnv(context.Background(), runtime.NewEnv(context.Background(), ir.Step{}))
	got := run(t, ctx, `return new Date(Date.UTC(2026, 10, 12, 17, 14, 0)).getTimezoneOffset()`, nil)
	require.NoError(t, got.err)
	assert.Equal(t, "-540\n", got.stdout)

	// Confirm Go process-wide time.Local itself was never mutated by js.Run.
	assert.Same(t, origLocal, time.Local)
}
