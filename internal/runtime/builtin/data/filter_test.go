// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package data

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listings are rows a page showed, each value as the page wrote it.
func listings(updated func(daysAgo int) string) string {
	rows := []map[string]string{
		{"name": "グランドメゾン天満橋", "price": "4,280万円", "access": "京阪本線「天満橋」徒歩4分", "built": "1983年5月", "address": "大阪市北区天満2", "updated": updated(2)},
		{"name": "ライオンズマンション本町", "price": "6,800万円", "access": "御堂筋線「本町」徒歩3分", "built": "1990年10月", "address": "大阪市中央区本町3", "updated": updated(5)},
		{"name": "南森町パークハイツ", "price": "5,500万円", "access": "谷町線「南森町」徒歩5分", "built": "昭和57年11月", "address": "大阪市北区南森町1", "updated": updated(0)},
		{"name": "淀川リバーサイド", "price": "3,200万円", "access": "阪急「十三」徒歩12分", "built": "1979年3月", "address": "大阪市淀川区十三本町1", "updated": updated(1)},
		{"name": "梅田タワー", "price": "1億2,000万円", "access": "「梅田」徒歩2分", "built": "2008年1月", "address": "大阪市北区梅田1", "updated": updated(3)},
		{"name": "天神橋ハイツ", "price": "価格未定", "access": "「天神橋筋六丁目」徒歩1分", "built": "1985年4月", "address": "大阪市北区天神橋6", "updated": updated(1)},
		{"name": "本町ガーデン", "price": "4,980万円", "access": "「本町」徒歩4分", "built": "2001年3月", "address": "大阪市中央区本町1", "updated": updated(30)},
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func daysAgo(n int) string {
	return "情報更新日：" + time.Now().AddDate(0, 0, -n).Format("2006/01/02")
}

func runDataFilter(t *testing.T, cfg map[string]any) (stdout, stderr string) {
	t.Helper()
	exec, err := newExecutor(context.Background(), dataStep(opFilter, cfg))
	require.NoError(t, err)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	exec.SetStdout(out)
	exec.SetStderr(errOut)
	require.NoError(t, exec.Run(context.Background()))
	return out.String(), errOut.String()
}

func names(t *testing.T, out string) []string {
	t.Helper()
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row["name"].(string))
	}
	return got
}

// A filter reads each value as the page wrote it, as a number, a date, or
// text, keeps the rows every condition holds for, and orders them.
func TestFilterReadsValuesAsWritten(t *testing.T) {
	t.Parallel()

	out, stderr := runDataFilter(t, map[string]any{
		"from": "json",
		"data": listings(daysAgo),
		"where": map[string]any{
			"price":   map[string]any{"max": 70000000},
			"access":  map[string]any{"max": 5},
			"built":   map[string]any{"from": "1981-06"},
			"updated": map[string]any{"within": "7d"},
			"address": map[string]any{"any": []any{"北区", "中央区"}},
		},
		"sort": "built",
	})
	assert.Equal(t, []string{"南森町パークハイツ", "グランドメゾン天満橋", "ライオンズマンション本町"}, names(t, out))
	assert.Contains(t, stderr, "kept 3 of 7 rows")
	assert.Contains(t, stderr, `left out 1 rows whose price did not read as a number, such as "価格未定"`)
}

func TestFilterConditions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		where map[string]any
		sort  any
		limit int
		want  []string
	}{
		{name: "MinOfJapaneseUnits", where: map[string]any{"price": map[string]any{"min": "1億"}}, want: []string{"梅田タワー"}},
		{name: "DateRangeToTheEndOfItsMonth", where: map[string]any{"built": map[string]any{"from": "1983-05", "to": "1990-10"}}, sort: "built", want: []string{"グランドメゾン天満橋", "天神橋ハイツ", "ライオンズマンション本町"}},
		{name: "IsAndNone", where: map[string]any{"address": map[string]any{"none": []any{"北区", "淀川区"}}}, want: []string{"ライオンズマンション本町", "本町ガーデン"}},
		{name: "FullWidthText", where: map[string]any{"name": map[string]any{"is": "梅田タワー"}}, want: []string{"梅田タワー"}},
		{name: "SortDescendingAndLimit", where: map[string]any{"price": map[string]any{"min": 0}}, sort: []any{"-price"}, limit: 2, want: []string{"梅田タワー", "ライオンズマンション本町"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := map[string]any{"from": "json", "data": listings(daysAgo), "where": tc.where, "limit": tc.limit}
			if tc.sort != nil {
				cfg["sort"] = tc.sort
			}
			out, _ := runDataFilter(t, cfg)
			assert.Equal(t, tc.want, names(t, out))
		})
	}
}

// A filter given an object picks its list of rows with select.
func TestFilterSelectsItsRows(t *testing.T) {
	t.Parallel()

	out, _ := runDataFilter(t, map[string]any{
		"from":   "json",
		"data":   `{"listings":` + listings(daysAgo) + `}`,
		"select": ".listings",
		"where":  map[string]any{"price": map[string]any{"max": 35000000}},
	})
	assert.Equal(t, []string{"淀川リバーサイド"}, names(t, out))
}

func TestFilterValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		where any
		want  string
	}{
		{name: "NoWhere", where: map[string]any{}, want: "where must name at least one field"},
		{name: "UnknownCondition", where: map[string]any{"price": map[string]any{"below": 3}}, want: `unknown condition "below"`},
		{name: "MixedKinds", where: map[string]any{"price": map[string]any{"max": 3, "any": "万"}}, want: "mixes number, date, and text conditions"},
		{name: "BadDate", where: map[string]any{"built": map[string]any{"from": "someday"}}, want: "from must be a date"},
		{name: "BadWithin", where: map[string]any{"updated": map[string]any{"within": "soon"}}, want: "within must be a number of days"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(dataStep(opFilter, map[string]any{"from": "json", "data": "[]", "where": tc.where}))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// A condition may hold a reference when the DAG loads, which resolves when
// the step runs; one still unresolved then fails the step rather than
// filtering nothing.
func TestFilterReferenceResolvesWhenTheStepRuns(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{"from": "json", "data": "[]", "where": map[string]any{"price": map[string]any{"max": "${params.MAX_PRICE}"}}}
	require.NoError(t, validateStep(dataStep(opFilter, cfg)))
	exec, err := newExecutor(context.Background(), dataStep(opFilter, cfg))
	require.NoError(t, err)
	exec.SetStdout(&bytes.Buffer{})
	exec.SetStderr(&bytes.Buffer{})
	require.ErrorContains(t, exec.Run(context.Background()), "max must be a number, got ${params.MAX_PRICE}")
}

func TestParseNumberAndDate(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]float64{"4,280万円": 42800000, "1億2,000万円": 120000000, "徒歩4分": 4, "１２，３００円": 12300, "6.5万": 65000} {
		got, ok := parseNumber(text)
		assert.True(t, ok, text)
		assert.InDelta(t, want, got, 0.001, text)
	}
	_, ok := parseNumber("価格未定")
	assert.False(t, ok)

	for text, want := range map[string]string{"1983年5月": "1983-05-01", "昭和58年5月": "1983-05-01", "平成元年": "1989-01-01", "情報更新日：2026/11/12": "2026-11-12", "築2001年": "2001-01-01"} {
		got, ok := parseDate(text)
		assert.True(t, ok, text)
		assert.Equal(t, want, got.start().Format("2006-01-02"), text)
	}
}
