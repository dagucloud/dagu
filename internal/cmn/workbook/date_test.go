// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every dated form spec 077 lists under Japanese text under a pinned
// type, and what it says is not read. Year-less forms have their own test
// below, since they read the clock.
func TestParseDateText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		// Kanji dates, digits Arabic or kanji.
		{"2026年10月3日", "2026-10-03T00:00:00"}, {"2026年10月3", "2026-10-03T00:00:00"}, {"２０２６年１０月３日", "2026-10-03T00:00:00"},
		{"二〇二六年十月三日", "2026-10-03T00:00:00"}, {"二千二十六年十月三日", "2026-10-03T00:00:00"}, {"2026年 10月 3日", "2026-10-03T00:00:00"},
		// Month only.
		{"2026年10月", "2026-10-01T00:00:00"}, {"2026/10", "2026-10-01T00:00:00"}, {"2026.10", "2026-10-01T00:00:00"}, {"2026-10", "2026-10-01T00:00:00"},
		// Numeric with one separator throughout.
		{"2026/10/3", "2026-10-03T00:00:00"}, {"2026.10.3", "2026-10-03T00:00:00"}, {"2026-10-3", "2026-10-03T00:00:00"},
		// Era, long.
		{"令和8年10月3日", "2026-10-03T00:00:00"}, {"令和元年5月1日", "2019-05-01T00:00:00"}, {"令和八年十月三日", "2026-10-03T00:00:00"},
		{"平成三十一年四月三十日", "2019-04-30T00:00:00"}, {"㋿8年10月3日", "2026-10-03T00:00:00"}, {"㍻31年4月30日", "2019-04-30T00:00:00"},
		{"昭和64年1月7日", "1989-01-07T00:00:00"}, {"大正15年12月24日", "1926-12-24T00:00:00"}, {"明治元年1月25日", "1868-01-25T00:00:00"}, {"平成元年1月8日", "1989-01-08T00:00:00"},
		// Era, short.
		{"R8.10.3", "2026-10-03T00:00:00"}, {"r8/10/3", "2026-10-03T00:00:00"}, {"H31-4-30", "2019-04-30T00:00:00"}, {"令6.4.1", "2024-04-01T00:00:00"}, {"Ｒ８．１０．３", "2026-10-03T00:00:00"},
		// Era month only.
		{"令和8年10月", "2026-10-01T00:00:00"}, {"R8.10", "2026-10-01T00:00:00"}, {"R8/10", "2026-10-01T00:00:00"},
		// Weekday marks dropped.
		{"2026年10月3日（金）", "2026-10-03T00:00:00"}, {"令和8年10月3日(金曜日)", "2026-10-03T00:00:00"}, {"2026/10/3 (Fri)", "2026-10-03T00:00:00"}, {"2026年10月3日 金曜日", "2026-10-03T00:00:00"},
		// Times of day.
		{"2026/10/3 14:30", "2026-10-03T14:30:00"}, {"2026.10.3 14:30:15", "2026-10-03T14:30:15"}, {"2026年10月3日14時", "2026-10-03T14:00:00"},
		{"2026年10月3日 14時30分", "2026-10-03T14:30:00"}, {"令和8年10月3日 14時30分15秒", "2026-10-03T14:30:15"}, {"2026年10月3日（金） 14:30", "2026-10-03T14:30:00"},
		{"2026年10月3日(金)14:30", "2026-10-03T14:30:00"},
		// 午前 and 午後.
		{"2026年10月3日 午後2時30分", "2026-10-03T14:30:00"}, {"2026年10月3日 午前9時", "2026-10-03T09:00:00"}, {"2026年10月3日 午後0時", "2026-10-03T12:00:00"},
		{"2026年10月3日 午後12時", "2026-10-03T12:00:00"}, {"2026年10月3日 午前0時", "2026-10-03T00:00:00"}, {"2026年10月3日 午前12時", "2026-10-03T00:00:00"},
	} {
		got, ok := parseDateText(foldWidth(tc.in))
		require.True(t, ok, tc.in)
		assert.Equal(t, tc.want, got.Format(dateTimeLayout), tc.in)
	}

	for _, in := range []string{
		"本日", "未定", "10月", "2026", "2026年", "R8", "令和8年13月", "10/32", "2026年10月3日 25:00", "2026年10月3日 24:00",
		"10.3", "8.10.3", "26/10/3", "2026/10-3", "2026/10/3/4", "令和8年10月3日頃", "2026年10月3日〜", "14:30", "午後2時",
		"2026年10月3日 午後14時", "2026年10月3日 2時半", "2026年10月3日 十四時三十分", "2026年10月3日 2:30 PM",
		"令和元年4月30日", "平成31年5月1日", "昭和64年1月8日", "令和0年1月1日", "令和8年2月30日", "2026年13月1日", "",
	} {
		_, ok := parseDateText(foldWidth(in))
		assert.False(t, ok, in)
	}
}

// A date with no year takes the year of the clock. This test replaces the
// clock, so it does not run in parallel.
func TestParseDateTextYearless(t *testing.T) {
	previous := now
	t.Cleanup(func() { now = previous })
	now = func() time.Time { return time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct{ in, want string }{
		{"10月3日", "2026-10-03T00:00:00"}, {"十月三日", "2026-10-03T00:00:00"}, {"10/3", "2026-10-03T00:00:00"}, {"１０／３", "2026-10-03T00:00:00"},
		{"10月3日（金） 14:30", "2026-10-03T14:30:00"}, {"3月1日", "2026-03-01T00:00:00"},
	} {
		got, ok := parseDateText(foldWidth(tc.in))
		require.True(t, ok, tc.in)
		assert.Equal(t, tc.want, got.Format(dateTimeLayout), tc.in)
	}
	_, ok := parseDateText("2月29日")
	assert.False(t, ok, "2026 is not a leap year")

	now = func() time.Time { return time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC) }
	got, ok := parseDateText("2月29日")
	require.True(t, ok)
	assert.Equal(t, "2028-02-29", got.Format(dateLayout))

	v, err := coerce("10月3日", TypeDate, false)
	require.NoError(t, err)
	assert.Equal(t, "2028-10-03", v, "coerce reads the clock too")
}

// Through coerce: a date pin drops the time, a datetime pin keeps it, and
// a failure quotes the cell's text.
func TestCoerceJapaneseDates(t *testing.T) {
	t.Parallel()
	v, err := coerce("令和8年10月3日 午後2時30分", TypeDate, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-03", v)
	v, err = coerce("令和8年10月3日 午後2時30分", TypeDateTime, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-03T14:30:00", v)
	v, err = coerce("2026年10月", TypeDate, false)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01", v)
	_, err = coerce("令和8年10月3日（金）頃", TypeDate, false)
	require.EqualError(t, err, `expected date, found "令和8年10月3日（金）頃"`)
	_, err = coerce("2:30 PM", TypeDateTime, false)
	require.EqualError(t, err, `expected datetime, found "2:30 PM"`)
}
