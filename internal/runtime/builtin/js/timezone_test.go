// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package js

import (
	"context"
	"sync"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
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

	// Kathmandu (+05:45) differs from any host zone the tests run in, so a
	// string read in the process zone instead of the step zone shows up.
	t.Run("ParseStringsInStepZone", func(t *testing.T) {
		t.Parallel()
		script := `
return {
  monthName: new Date("Nov 13 2026 02:14:00").toISOString(),
  spaced: new Date("2026-11-13 02:14:00").toISOString(),
  slashes: new Date("2026/11/13").toISOString(),
  extendedYear: new Date("+002026-11-13T02:14:00").toISOString(),
  paddedDateOnly: new Date(" 2026-11-13 ").toISOString(),
  gmt: new Date("Fri, 13 Nov 2026 02:14:00 GMT").toISOString(),
  gmtOffset: new Date("Fri Nov 13 2026 02:14:00 GMT+0900 (JST)").toISOString(),
  offset: new Date("Nov 13 2026 02:14:00 -0500").toISOString(),
  spacedOffset: new Date("2026-11-13 02:14:00+09:00").toISOString(),
  spacedFraction: new Date("2026-11-13 02:14:00.123456+00:00").toISOString(),
  spacedShortOffset: new Date("2026-11-13 02:14:00+00").toISOString(),
  spacedZ: new Date("2026-11-13 02:14:00Z").toISOString(),
  dayName: new Date("Thu 12-Nov-2026 02:14").toISOString(),
  parsed: Date.parse("Nov 13 2026 02:14:00"),
  invalid: Number.isNaN(Date.parse("2026-13-01T10:00:00")),
  customToString: new Date({ toString() { return "Nov 13 2026 02:14:00"; } }).toISOString(),
  noValueOf: new Date({ valueOf: undefined, toString() { return "Nov 13 2026 02:14:00"; } }).toISOString(),
  toPrimitiveHook: new Date({ [Symbol.toPrimitive]() { return "Nov 13 2026 02:14:00"; } }).toISOString()
}`
		got := run(t, ctxWithTZ("Asia/Kathmandu", nil), script, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `{
  "monthName": "2026-11-12T20:29:00.000Z",
  "spaced": "2026-11-12T20:29:00.000Z",
  "slashes": "2026-11-12T18:15:00.000Z",
  "extendedYear": "2026-11-12T20:29:00.000Z",
  "paddedDateOnly": "2026-11-12T18:15:00.000Z",
  "gmt": "2026-11-13T02:14:00.000Z",
  "gmtOffset": "2026-11-12T17:14:00.000Z",
  "offset": "2026-11-13T07:14:00.000Z",
  "spacedOffset": "2026-11-12T17:14:00.000Z",
  "spacedFraction": "2026-11-13T02:14:00.123Z",
  "spacedShortOffset": "2026-11-13T02:14:00.000Z",
  "spacedZ": "2026-11-13T02:14:00.000Z",
  "dayName": "2026-11-11T20:29:00.000Z",
  "parsed": 1794515340000,
  "invalid": true,
  "customToString": "2026-11-12T20:29:00.000Z",
  "noValueOf": "2026-11-12T20:29:00.000Z",
  "toPrimitiveHook": "2026-11-12T20:29:00.000Z"
}`, got.stdout)
	})

	t.Run("Subclass", func(t *testing.T) {
		t.Parallel()
		got := run(t, ctxWithTZ("Asia/Tokyo", nil), `
class Stamp extends Date {
  label() { return "h" + this.getHours(); }
}
const s = new Stamp(Date.UTC(2026, 10, 12, 17, 14, 0));
return [s instanceof Stamp, s instanceof Date, s.label(), s.toISOString()]`, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `[true, true, "h2", "2026-11-12T17:14:00.000Z"]`, got.stdout)
	})

	t.Run("MethodsMatchBuiltins", func(t *testing.T) {
		t.Parallel()
		got := run(t, ctxWithTZ("Asia/Tokyo", nil), `
const p = Date.prototype;
const thrown = (fn) => { try { fn(); } catch (e) { return e.name + ": " + e.message; } return "none"; };
return {
  names: [p.getHours.name, p.getHours.length, p.toString.name, p.setHours.name, p.setHours.length, p.setFullYear.length],
  staticKeys: Object.keys(Date),
  badReceiver: thrown(() => p.getHours.call({})),
  newParse: thrown(() => new Date.parse("2026")).split(":")[0]
}`, nil)
		require.NoError(t, got.err)
		assert.JSONEq(t, `{
  "names": ["getHours", 0, "toString", "setHours", 4, 3],
  "staticKeys": [],
  "badReceiver": "TypeError: Method Date.prototype.getHours is called on incompatible receiver",
  "newParse": "TypeError"
}`, got.stdout)
	})

	t.Run("FixedOffsetTZ", func(t *testing.T) {
		t.Parallel()
		// POSIX TZ offsets count hours west of UTC, so UTC+9 is a JS offset of
		// +540. An unknown zone falls back to UTC.
		for tz, want := range map[string]string{
			"UTC+9":      "540\n",
			"UTC-9":      "-540\n",
			"UTC-3:30":   "-210\n",
			"Not/A_Zone": "0\n",
		} {
			got := run(t, ctxWithTZ(tz, nil), `return new Date(Date.UTC(2026, 10, 12, 12, 0, 0)).getTimezoneOffset()`, nil)
			require.NoError(t, got.err, tz)
			assert.Equal(t, want, got.stdout, tz)
		}
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

// A host TZ reaches js.run through the OS entries of the env scope. The entry
// is set directly: Go reads the process TZ into time.Local on first use, so a
// process-level TZ would pass even if js.run ignored the env scope.
func TestJS_HostTZ(t *testing.T) {
	t.Parallel()
	scope := cmnvalue.NewEnvScope(nil, false).
		WithEntries(map[string]string{"TZ": "Asia/Kathmandu"}, cmnvalue.EnvSourceOS)
	ctx := runtime.WithEnv(context.Background(), runtime.Env{Scope: scope})
	got := run(t, ctx, `return new Date(Date.UTC(2026, 10, 12, 17, 14, 0)).getTimezoneOffset()`, nil)
	require.NoError(t, got.err)
	assert.Equal(t, "-345\n", got.stdout)
}
