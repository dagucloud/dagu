// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every number form spec 077 lists under Japanese text under a pinned
// type, and what it says is not read.
func TestParseNumberText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want float64
	}{
		// ASCII, unchanged.
		{"123", 123}, {"-12.5", -12.5}, {"+7", 7}, {".5", 0.5}, {"1e3", 1000},
		// Thousands separators in groups of three.
		{"１２３，４５６", 123456}, {"1,234,567.89", 1234567.89}, {"1、000", 1000},
		// Yen before or after, JPY either side.
		{"¥1,500", 1500}, {"￥123,000", 123000}, {"¥ 1,500", 1500}, {"JPY 1,000", 1000}, {"jpy1000", 1000},
		{"123,000円", 123000}, {"1,000 円", 1000}, {"1,000 JPY", 1000},
		// Receipt wrappers.
		{"金1,000円", 1000}, {"金壱万円也", 10000}, {"金一万也", 10000}, {"1,000円也", 1000},
		// The trailing dash.
		{"¥123,000-", 123000}, {"￥１２３，０００－", 123000}, {"123,000円-", 123000}, {"¥1,000ー", 1000}, {"¥1,000―", 1000}, {"¥1,000—", 1000},
		// Negatives, before or after the yen mark.
		{"-5", -5}, {"−5", -5}, {"－５", -5}, {"▲1,000", -1000}, {"△1,000", -1000}, {"ー５", -5},
		{"-¥1,000", -1000}, {"¥-1,000", -1000}, {"▲¥1,000", -1000}, {"¥▲1,000", -1000}, {"-1,000円", -1000}, {"▲1,000円", -1000},
		// Accounting parentheses.
		{"(1,000)", -1000}, {"（1,000）", -1000}, {"(¥1,000)", -1000}, {"(1,000円)", -1000},
		// Percent.
		{"10%", 10}, {"１０％", 10}, {"-10%", -10}, {"10.5%", 10.5},
		// Price tags.
		{"税込1,000", 1000}, {"税込：1,000", 1000}, {"合計 1,000", 1000}, {"合計金額：￥123,000", 123000}, {"小計¥1,000-", 1000},
		{"1,000（税込）", 1000}, {"1,000円(税抜)", 1000}, {"（税込）¥1,000", 1000}, {"¥1,000-（税込）", 1000}, {"1,000(概算)", 1000},
		// Kanji numerals.
		{"千", 1000}, {"百万", 1000000}, {"三千五百", 3500}, {"一万二千", 12000}, {"五千万", 50000000}, {"十", 10}, {"二十六", 26},
		{"壱拾弐万参千", 123000}, {"金壱拾弐萬参阡円也", 123000}, {"弐佰", 200}, {"参仟", 3000},
		{"二〇二六", 2026}, {"〇", 0}, {"零", 0}, {"一二三", 123},
		// Arabic digits with units.
		{"12万3500", 123500}, {"12万3,500円", 123500}, {"1,200万", 12000000}, {"1.5億", 150000000}, {"１．５億", 150000000},
		{"12.5万円", 125000}, {"1,200千円", 1200000}, {"3百万円", 3000000}, {"12万3千", 123000}, {"12万3千4", 123004},
		{"1.15万", 11500}, {"0.1万", 1000}, {"3千500", 3500},
		// Surrounding space, the ideographic space included.
		{"　42　", 42}, {" ¥1,000 ", 1000},
	} {
		got, ok := parseNumberText(tc.in)
		require.True(t, ok, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}

	for _, in := range []string{
		"abc", "￥abc", "約1,000", "1,000円以上", "1,000〜2,000", "1,000-2,000", "10:30", "2026年", "2026/10/3",
		"1,2,3", "1000,000", "１２３ ４５６", "1 000", "--5", "-(1,000)", "(1,000", "¥", "円", "▲", "%",
		"10%円", "1,000円%", "¥10%", "(税込)", "1,000(USD)", "1,000（千円）", "1,000也",
		"二〇二六万", "二三百", "万万", "3万2億", "12万3万", "五百三千", "五.五", "一、〇〇〇", "ー万", "", "▼1,000",
	} {
		_, ok := parseNumberText(in)
		assert.False(t, ok, in)
	}
}

// Through coerce: integral results are integers, fractions stay floats,
// an integer pin refuses a fraction, and a failure quotes the cell's text.
func TestCoerceJapaneseNumbers(t *testing.T) {
	t.Parallel()
	v, err := coerce("12万3,500円", TypeNumber, false)
	require.NoError(t, err)
	assert.Equal(t, int64(123500), v)
	v, err = coerce("10.5%", TypeNumber, false)
	require.NoError(t, err)
	assert.Equal(t, 10.5, v)
	v, err = coerce("1.5億", TypeInteger, false)
	require.NoError(t, err)
	assert.Equal(t, int64(150000000), v)
	_, err = coerce("10.5%", TypeInteger, false)
	require.EqualError(t, err, `expected integer, found "10.5%"`)
	_, err = coerce("約1,000", TypeNumber, false)
	require.EqualError(t, err, `expected number, found "約1,000"`)
	_, err = coerce("NaN", TypeNumber, false)
	require.Error(t, err)
	for _, in := range []string{"1,234", "￥123,000", "123,000円", "7"} {
		_, ok := toFloat(in)
		assert.True(t, ok, in)
	}
	for _, in := range []string{"10%", "¥1,000-", "12万", "▲1,000"} {
		_, ok := toFloat(in)
		assert.False(t, ok, "where and header matching keep the narrow reading: %s", in)
	}
}
