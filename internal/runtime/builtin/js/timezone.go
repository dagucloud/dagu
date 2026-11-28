// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package js

import (
	"context"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Ensure IANA zones resolve inside containers and on Windows when TZ is set.

	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dop251/goja"
)

// isoDatePattern is the ECMAScript date-time string format's date part.
const isoDatePattern = `([+-]\d{6}|\d{4})(-\d{2}(-\d{2})?)?`

var (
	isoDateOnlyPattern = regexp.MustCompile(`^` + isoDatePattern + `$`)
	// isoLocalDateTimePattern matches date-times without an offset, which
	// ECMAScript reads as local time.
	isoLocalDateTimePattern = regexp.MustCompile(`^` + isoDatePattern + `T\d{2}:\d{2}(:\d{2}([.,]\d+)?)?$`)
	// zoneAfterTimePattern matches a "Z" or numeric offset right after a
	// time, as in "2026-11-13T10:00Z" or "2026-11-13 10:00:00+09:00".
	zoneAfterTimePattern = regexp.MustCompile(`(?i)\d:\d{2}(:\d{2}([.,]\d+)?)?\s*(z\b|[+-]\d)`)
)

const (
	dateTimeLayout          = "Mon Jan 02 2006 15:04:05 GMT-0700 (MST)"
	dateLayout              = "Mon Jan 02 2006"
	timeLayout              = "15:04:05 GMT-0700 (MST)"
	localeDateTimeLayout    = "01/02/2006, 15:04:05"
	localeDateLayout        = "01/02/2006"
	localeTimeLayout        = "15:04:05"
	invalidDateString       = "Invalid Date"
	maxECMAScriptTimeMillis = 8.64e15
)

var tzTokenSet = map[string]struct{}{
	"gmt": {}, "utc": {}, "ut": {}, "z": {},
	"edt": {}, "est": {}, "cdt": {}, "cst": {},
	"mdt": {}, "mst": {}, "pdt": {}, "pst": {},
	"wet": {}, "west": {}, "cet": {}, "cest": {},
	"eet": {}, "eest": {},
}

// resolveTimezone returns the step/DAG/process TZ location when TZ is configured
// in the execution scope or process environment.
func resolveTimezone(ctx context.Context) (*time.Location, bool) {
	if env, ok := runtime.LookupEnv(ctx); ok && env.Scope != nil {
		if tz, set := env.Scope.Get("TZ"); set {
			return parseTZLocation(tz), true
		}
		return nil, false
	}
	if tz, set := os.LookupEnv("TZ"); set {
		return parseTZLocation(tz), true
	}
	return nil, false
}

// parseTZLocation resolves TZ to a *time.Location following POSIX/Go semantics:
// empty or invalid TZ falls back to UTC, matching Go's initLocal behavior.
func parseTZLocation(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	name := strings.TrimPrefix(tz, ":")
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	if loc, ok := parseFixedOffsetTZ(name); ok {
		return loc
	}
	return time.UTC
}

func parseFixedOffsetTZ(name string) (*time.Location, bool) {
	var rest string
	switch {
	case strings.HasPrefix(name, "UTC"):
		rest = name[3:]
	case strings.HasPrefix(name, "GMT"):
		rest = name[3:]
	default:
		return nil, false
	}
	if rest == "" {
		return time.UTC, true
	}
	// POSIX TZ offsets specify hours added to local time to get UTC,
	// so UTC+9 is 9 hours west of UTC (-9*3600) and UTC-9 is east (+9*3600).
	sign := -1
	switch rest[0] {
	case '+':
	case '-':
		sign = 1
	default:
		return nil, false
	}
	hoursPart, minsPart, hasColon := strings.Cut(rest[1:], ":")
	if hoursPart == "" {
		return nil, false
	}
	hours, err := strconv.Atoi(hoursPart)
	if err != nil || hours < 0 || hours > 23 {
		return nil, false
	}
	mins := 0
	if hasColon {
		mins, err = strconv.Atoi(minsPart)
		if err != nil || mins < 0 || mins > 59 {
			return nil, false
		}
	}
	offset := sign * (hours*3600 + mins*60)
	return time.FixedZone(name, offset), true
}

// installDateTimezone binds Date and local Date.prototype methods in vm to loc
// without mutating process-wide time.Local, keeping concurrent steps isolated.
func installDateTimezone(vm *goja.Runtime, loc *time.Location) error {
	origDate := vm.Get("Date").ToObject(vm)
	proto := origDate.Get("prototype").ToObject(vm)
	origGetTime, _ := goja.AssertFunction(proto.Get("getTime"))
	origSetTime, _ := goja.AssertFunction(proto.Get("setTime"))
	origParse, _ := goja.AssertFunction(origDate.Get("parse"))

	// extractTime reads this in loc and reports false for an invalid date. A
	// receiver that is not a Date throws the TypeError the builtin method throws.
	extractTime := func(method string, this goja.Value) (time.Time, bool) {
		val, err := origGetTime(this)
		if err != nil {
			panic(vm.NewTypeError("Method Date.prototype.%s is called on incompatible receiver", method))
		}
		if goja.IsNaN(val) {
			return time.Time{}, false
		}
		return time.UnixMilli(val.ToInteger()).In(loc), true
	}

	// setMethod installs fn with the name and length the builtin reports.
	setMethod := func(method string, length int, fn func(goja.FunctionCall) goja.Value) {
		f := vm.ToValue(fn).ToObject(vm)
		_ = f.DefineDataProperty("name", vm.ToValue(method), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
		_ = f.DefineDataProperty("length", vm.ToValue(length), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
		_ = proto.Set(method, f)
	}

	readField := func(method string, fn func(time.Time) goja.Value) {
		setMethod(method, 0, func(call goja.FunctionCall) goja.Value {
			t, valid := extractTime(method, call.This)
			if !valid {
				return goja.NaN()
			}
			return fn(t)
		})
	}

	readString := func(method string, layout string) {
		setMethod(method, 0, func(call goja.FunctionCall) goja.Value {
			t, valid := extractTime(method, call.This)
			if !valid {
				return vm.ToValue(invalidDateString)
			}
			return vm.ToValue(t.Format(layout))
		})
	}

	readField("getFullYear", func(t time.Time) goja.Value { return vm.ToValue(t.Year()) })
	readField("getMonth", func(t time.Time) goja.Value { return vm.ToValue(int(t.Month()) - 1) })
	readField("getDate", func(t time.Time) goja.Value { return vm.ToValue(t.Day()) })
	readField("getDay", func(t time.Time) goja.Value { return vm.ToValue(int(t.Weekday())) })
	readField("getHours", func(t time.Time) goja.Value { return vm.ToValue(t.Hour()) })
	readField("getMinutes", func(t time.Time) goja.Value { return vm.ToValue(t.Minute()) })
	readField("getSeconds", func(t time.Time) goja.Value { return vm.ToValue(t.Second()) })
	readField("getMilliseconds", func(t time.Time) goja.Value { return vm.ToValue(t.Nanosecond() / 1e6) })
	readField("getTimezoneOffset", func(t time.Time) goja.Value {
		_, offset := t.Zone()
		return vm.ToValue(float64(-offset) / 60)
	})

	readString("toString", dateTimeLayout)
	readString("toDateString", dateLayout)
	readString("toTimeString", timeLayout)
	readString("toLocaleString", localeDateTimeLayout)
	readString("toLocaleDateString", localeDateLayout)
	readString("toLocaleTimeString", localeTimeLayout)

	setFields := func(method string, maxArgs int, startFromEpochOnInvalid bool, startIdx int) {
		setMethod(method, maxArgs, func(call goja.FunctionCall) goja.Value {
			t, valid := extractTime(method, call.This)
			if !valid {
				if !startFromEpochOnInvalid {
					return goja.NaN()
				}
				t = time.Date(1970, time.January, 1, 0, 0, 0, 0, loc)
			}
			args := call.Arguments
			if len(args) > maxArgs {
				args = args[:maxArgs]
			}
			ms, ok := applyDateFields(t, loc, startIdx, args)
			var next goja.Value
			if !ok {
				next = goja.NaN()
			} else {
				next = vm.ToValue(ms)
			}
			res, err := origSetTime(call.This, next)
			if err != nil {
				panic(err)
			}
			return res
		})
	}

	// Field indices: 0=year, 1=month, 2=day, 3=hours, 4=minutes, 5=seconds, 6=ms.
	setFields("setFullYear", 3, true, 0)
	setFields("setMonth", 2, false, 1)
	setFields("setDate", 1, false, 2)
	setFields("setHours", 4, false, 3)
	setFields("setMinutes", 3, false, 4)
	setFields("setSeconds", 2, false, 5)
	setFields("setMilliseconds", 1, false, 6)

	nowStr := func() string {
		return time.Now().In(loc).Format(dateTimeLayout)
	}
	makeLocalMs := func(args []goja.Value) goja.Value {
		if len(args) == 0 {
			return goja.NaN()
		}
		if len(args) > 7 {
			args = args[:7]
		}
		var fields [7]int64
		fields[0] = 1970
		fields[2] = 1
		for i, v := range args {
			num, ok := finiteIntArg(v)
			if !ok {
				return goja.NaN()
			}
			if i == 0 && num >= 0 && num <= 99 {
				num += 1900
			}
			fields[i] = num
		}
		ms, ok := makeLocalTimeMillis(fields, loc)
		if !ok {
			return goja.NaN()
		}
		return vm.ToValue(ms)
	}
	parse := func(s string) goja.Value {
		parsed, err := origParse(goja.Undefined(), vm.ToValue(s))
		if err != nil {
			panic(err)
		}
		return parsed
	}
	parseLocalMs := func(s string) goja.Value {
		// A zone-less string is parsed with a UTC designator appended, which
		// yields its wall clock without passing through the process zone.
		var wall goja.Value
		switch {
		case isoLocalDateTimePattern.MatchString(s):
			wall = parse(s + "Z")
		case hasExplicitTimezone(s):
			return parse(s)
		default:
			wall = parse(s + " UTC")
		}
		if goja.IsNaN(wall) {
			return wall
		}
		ms, ok := makeLocalTimeMillis(dateFields(time.UnixMilli(wall.ToInteger()).UTC()), loc)
		if !ok {
			return goja.NaN()
		}
		return vm.ToValue(ms)
	}

	factoryVal, err := vm.RunString(`(function(OrigDate, nowStr, makeLocalMs, parseLocalMs) {
  // Captured so scripts that reassign Reflect or Symbol keep a working Date.
  var construct = Reflect.construct;
  var apply = Reflect.apply;
  var toPrimitiveKey = Symbol.toPrimitive;
  function isObject(v) {
    return (typeof v === 'object' && v !== null) || typeof v === 'function';
  }
  // toPrimitive is the hint-less ToPrimitive that Date applies to a single
  // argument that is not a Date.
  function toPrimitive(v) {
    if (!isObject(v)) {
      return v;
    }
    var result;
    var exotic = v[toPrimitiveKey];
    if (exotic !== undefined && exotic !== null) {
      result = apply(exotic, v, ['default']);
      if (!isObject(result)) {
        return result;
      }
    } else {
      var names = ['valueOf', 'toString'];
      for (var i = 0; i < names.length; i++) {
        var fn = v[names[i]];
        if (typeof fn === 'function') {
          result = apply(fn, v, []);
          if (!isObject(result)) {
            return result;
          }
        }
      }
    }
    throw new TypeError('Cannot convert object to primitive value');
  }
  function Date(year, month, day, hours, minutes, seconds, ms) {
    if (new.target === undefined) {
      return nowStr();
    }
    // Constructing through new.target keeps subclass prototypes.
    var len = arguments.length;
    if (len === 0) {
      return construct(OrigDate, [], new.target);
    }
    if (len === 1) {
      var arg = arguments[0];
      if (arg instanceof OrigDate) {
        return construct(OrigDate, [arg], new.target);
      }
      var prim = toPrimitive(arg);
      if (typeof prim === 'string') {
        return construct(OrigDate, [parseLocalMs(prim)], new.target);
      }
      return construct(OrigDate, [prim], new.target);
    }
    return construct(OrigDate, [makeLocalMs(Array.prototype.slice.call(arguments))], new.target);
  }
  Date.prototype = OrigDate.prototype;
  Object.defineProperty(OrigDate.prototype, 'constructor', {
    value: Date,
    writable: true,
    enumerable: false,
    configurable: true
  });
  // Static methods stay non-enumerable, like the builtin ones.
  function defineStatic(name, fn) {
    Object.defineProperty(Date, name, {
      value: fn,
      writable: true,
      enumerable: false,
      configurable: true
    });
  }
  defineStatic('UTC', OrigDate.UTC);
  defineStatic('now', OrigDate.now);
  // Method syntax keeps parse non-constructible, like the builtin.
  defineStatic('parse', {
    parse(str) {
      return parseLocalMs(String(str));
    }
  }.parse);
  return Date;
})`)
	if err != nil {
		return err
	}
	factory, _ := goja.AssertFunction(factoryVal)
	wrapped, err := factory(goja.Undefined(), origDate, vm.ToValue(nowStr), vm.ToValue(makeLocalMs), vm.ToValue(parseLocalMs))
	if err != nil {
		return err
	}
	return vm.Set("Date", wrapped)
}

func finiteIntArg(v goja.Value) (int64, bool) {
	num := v.ToNumber()
	f := num.ToFloat()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return num.ToInteger(), true
}

func applyDateFields(t time.Time, loc *time.Location, startIdx int, args []goja.Value) (int64, bool) {
	if len(args) == 0 {
		return 0, false
	}
	fields := dateFields(t)
	for i, arg := range args {
		idx := startIdx + i
		if idx >= len(fields) {
			break
		}
		v, ok := finiteIntArg(arg)
		if !ok {
			return 0, false
		}
		fields[idx] = v
	}
	return makeLocalTimeMillis(fields, loc)
}

// dateFields returns t's year, zero-based month, day, hours, minutes,
// seconds, and milliseconds.
func dateFields(t time.Time) [7]int64 {
	return [7]int64{
		int64(t.Year()),
		int64(t.Month()) - 1,
		int64(t.Day()),
		int64(t.Hour()),
		int64(t.Minute()),
		int64(t.Second()),
		int64(t.Nanosecond() / 1e6),
	}
}

func makeLocalTimeMillis(fields [7]int64, loc *time.Location) (int64, bool) {
	year, month, ok := normPair(fields[0], fields[1], 12)
	if !ok {
		return 0, false
	}
	sec, msec, ok := normPair(fields[5], fields[6], 1000)
	if !ok {
		return 0, false
	}
	nsec := msec * 1e6
	min, sec, ok := normPair(fields[4], sec, 60)
	if !ok {
		return 0, false
	}
	hour, min, ok := normPair(fields[3], min, 60)
	if !ok {
		return 0, false
	}
	day, hour, ok := normPair(fields[2], hour, 24)
	if !ok {
		return 0, false
	}
	if year > math.MaxInt32 || year < math.MinInt32 ||
		day > math.MaxInt32 || day < math.MinInt32 ||
		month >= math.MaxInt32 || month < math.MinInt32-1 {
		return 0, false
	}
	constructed := time.Date(int(year), time.Month(month)+1, int(day), int(hour), int(min), int(sec), int(nsec), loc)
	ms := constructed.UnixMilli()
	if math.Abs(float64(ms)) > maxECMAScriptTimeMillis {
		return 0, false
	}
	return ms, true
}

func normPair(hi, lo, base int64) (int64, int64, bool) {
	if lo < 0 {
		if hi == math.MinInt64 && lo <= -base {
			return 0, 0, false
		}
		n := (-lo-1)/base + 1
		hi -= n
		lo += n * base
	}
	if lo >= base {
		if hi == math.MaxInt64 {
			return 0, 0, false
		}
		n := lo / base
		hi += n
		lo -= n * base
	}
	return hi, lo, true
}

func hasExplicitTimezone(s string) bool {
	if zoneAfterTimePattern.MatchString(s) {
		return true
	}
	// ISO date-only forms ("2026", "2026-11", "2026-11-13") parse as UTC per ES spec.
	if isoDateOnlyPattern.MatchString(s) {
		return true
	}
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == ',' || r == '(' || r == ')'
	})
	for _, tok := range fields {
		if _, ok := tzTokenSet[tok]; ok {
			return true
		}
		if strings.HasPrefix(tok, "gmt") || strings.HasPrefix(tok, "utc") {
			return true
		}
		if len(tok) >= 2 && (tok[0] == '+' || tok[0] == '-') && tok[1] >= '0' && tok[1] <= '9' {
			return true
		}
	}
	return false
}
