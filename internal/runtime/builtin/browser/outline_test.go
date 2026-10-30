// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSnapshot reads a page's accessibility tree, as Chrome reported it,
// and its link addresses from testdata/outline.
func fixtureSnapshot(t *testing.T, name string) pageSnapshot {
	t.Helper()
	tree, err := os.ReadFile(filepath.Join("testdata", "outline", name+".tree"))
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join("testdata", "outline", name+".urls.json"))
	require.NoError(t, err)
	var urls map[string]string
	require.NoError(t, json.Unmarshal(data, &urls))
	return pageSnapshot{Tree: string(tree), URLs: urls}
}

// The outline shows what a person decides from: headings, fields, buttons,
// links with their addresses, messages, and tables and lists with repeated
// rows collapsed, nested in the landmarks and frames that hold them, with
// the page's own content before the site's header, menus, and footer.
func TestOutlineDescribesWhatAPersonSees(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ fixture, want string }{
		{"login", `heading: 取引先ポータル
form
  [0-4] textbox "ログインID"
  [0-5] textbox "パスワード"
  [0-6] checkbox "Remember me" [checked]
  [0-7] button "ログイン"
alert: IDまたはパスワードが違います。
banner
  navigation
    [0-15] link "Home" -> https://portal.example.com/
    [0-17] link "Help" -> https://portal.example.com/help`},
		{"orders", `heading: 注文一覧
[0-106] select "状態" = 未出荷; options: すべて, 未出荷, 出荷済み
[0-131] button "検索"
table: 12 rows; columns: 注文番号 | 取引先 | 金額 | 状態
  row: PO-01 | Acme | 12,000円 | 未出荷
    [0-142] link "PO-01" -> https://portal.example.com/orders/PO-01
  row: PO-02 | Acme | 12,000円 | 未出荷
    [0-148] link "PO-02" -> https://portal.example.com/orders/PO-02
  row: PO-03 | Acme | 12,000円 | 未出荷
    [0-154] link "PO-03" -> https://portal.example.com/orders/PO-03
  … 9 more rows like these
[0-213] button "前へ"
text: 12件中 1 / 3 ページ
[0-215] button "次へ"
navigation
  [0-112] link "注文一覧" -> https://portal.example.com/orders
  [0-113] link "請求書" -> https://portal.example.com/invoices`},
		{"list", `heading: Items
list
  item: Item B ¥100
    [0-364] link "Item B" -> https://portal.example.com/item/b
  item: Item C ¥200
    [0-367] link "Item C" -> https://portal.example.com/item/c
  item: Item D ¥300
    [0-370] link "Item D" -> https://portal.example.com/item/d
  … 27 more items like these
[0-354] radio "Newest" [checked]
[0-355] radio "Price"
[0-229] textbox "Note"`},
		{"frame", `heading: Outer
iframe "login frame"
  heading: 取引先ポータル
  form
    [1-469] textbox "ログインID"
    [1-470] textbox "パスワード"
    [1-471] checkbox "Remember me" [checked]
    [1-472] button "ログイン"
  alert: IDまたはパスワードが違います。
  banner
    navigation
      [1-481] link "Home" -> https://portal.example.com/
      [1-483] link "Help" -> https://portal.example.com/help`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			text, truncated, _ := renderOutline(fixtureSnapshot(t, tc.fixture), outlineOptions{MaxChars: 8000})
			assert.False(t, truncated)
			assert.Equal(t, tc.want, text)
		})
	}
}

// A search shows every entry containing the text, whatever its case and
// however deep in a collapsed run, with the entries it sits in.
func TestOutlineFind(t *testing.T) {
	t.Parallel()

	snap := fixtureSnapshot(t, "orders")
	text, _, matches := renderOutline(snap, outlineOptions{Find: "po-07"})
	assert.Equal(t, 1, matches)
	assert.Equal(t, `table: 12 rows; columns: 注文番号 | 取引先 | 金額 | 状態
  row: PO-07 | Acme | 12,000円 | 未出荷
    [0-178] link "PO-07" -> https://portal.example.com/orders/PO-07`, text)

	text, _, matches = renderOutline(snap, outlineOptions{Find: "orders/PO-1"})
	assert.Equal(t, 3, matches, "the address of a link is searched too")
	assert.Equal(t, 1, strings.Count(text, "table:"), "matches in one table share its line")

	text, _, matches = renderOutline(snap, outlineOptions{Find: "invoice #42"})
	assert.Zero(t, matches)
	assert.Empty(t, text)
}

// An outline over its limit ends at a whole line and says how to see more.
func TestOutlineFitsItsLimit(t *testing.T) {
	t.Parallel()

	text, truncated, _ := renderOutline(fixtureSnapshot(t, "orders"), outlineOptions{MaxChars: 200})
	assert.True(t, truncated)
	assert.LessOrEqual(t, utf8.RuneCountInString(text), 200+utf8.RuneCountInString("… outline cut: 99 more lines; narrow it with find or allow more characters"))
	lines := strings.Split(text, "\n")
	assert.Equal(t, `heading: 注文一覧`, lines[0], "the page's content comes before the site's menus")
	assert.Regexp(t, `^… outline cut: \d+ more lines; narrow it with find or allow more characters$`, lines[len(lines)-1])
}

// What was typed into a field never shows, whether the field holds it as
// text or names it as its value.
func TestOutlineLeavesOutTypedText(t *testing.T) {
	t.Parallel()

	tree := `[0-1] RootWebArea: Sign in
  [0-2] textbox: Password
    [0-3] StaticText: s3cret-value
  [0-4] combobox: Search
    [0-5] StaticText: typed query
  [0-6] paragraph
    [0-7] StaticText: Welcome back`
	text, _, _ := renderOutline(pageSnapshot{Tree: tree}, outlineOptions{})
	assert.Equal(t, `[0-2] textbox "Password"
[0-4] combobox "Search"
text: Welcome back`, text)
}

// A select lists its first options and counts the rest, and a link that
// runs script shows no address.
func TestOutlineChoicesAndScriptLinks(t *testing.T) {
	t.Parallel()

	var tree strings.Builder
	tree.WriteString("[0-1] RootWebArea: Settings\n  [0-2] select: Country\n")
	for i := range 20 {
		flag := ""
		if i == 4 {
			flag = " [selected]"
		}
		fmt.Fprintf(&tree, "    [0-%d] option: Country %d%s\n", 10+i, i, flag)
	}
	tree.WriteString("  [0-3] link: Open menu\n  [0-4] link: Name: with colon\n")
	text, _, _ := renderOutline(pageSnapshot{Tree: tree.String(), URLs: map[string]string{
		"0-3": "javascript:void(0)",
		"0-4": "https://example.com/a",
	}}, outlineOptions{})
	assert.Equal(t, `[0-2] select "Country" = Country 4; options: Country 0, Country 1, Country 2, Country 3, Country 4, Country 5, Country 6, Country 7, Country 8, Country 9, Country 10, Country 11, Country 12, Country 13, Country 14 (+5 more)
[0-3] link "Open menu"
[0-4] link "Name: with colon" -> https://example.com/a`, text)
}

// A tree whose lines end in CRLF, as a fixture checked out on Windows has,
// reads as the same tree.
func TestOutlineReadsCRLFTrees(t *testing.T) {
	t.Parallel()

	snap := fixtureSnapshot(t, "login")
	want, _, _ := renderOutline(snap, outlineOptions{})
	snap.Tree = strings.ReplaceAll(strings.ReplaceAll(snap.Tree, "\r\n", "\n"), "\n", "\r\n")
	got, _, _ := renderOutline(snap, outlineOptions{})
	assert.Equal(t, want, got)
}

// The limit counts characters, not bytes, so a page in Japanese keeps as
// many lines as fit in that many characters.
func TestOutlineLimitCountsCharacters(t *testing.T) {
	t.Parallel()

	snap := fixtureSnapshot(t, "orders")
	full, _, _ := renderOutline(snap, outlineOptions{})
	kept := strings.Join(strings.Split(full, "\n")[:6], "\n")
	require.Contains(t, kept, "注文一覧")
	text, truncated, _ := renderOutline(snap, outlineOptions{MaxChars: utf8.RuneCountInString(kept) + 1})
	assert.True(t, truncated)
	assert.True(t, strings.HasPrefix(text, kept+"\n… outline cut: "), text)
}

// A long run of alike fields is counted by their kind's plural.
func TestOutlineCountsAlikeFields(t *testing.T) {
	t.Parallel()

	var tree strings.Builder
	tree.WriteString("[0-1] RootWebArea: Survey\n  [0-2] form\n")
	for i := range 7 {
		fmt.Fprintf(&tree, "    [0-%d] checkbox: Option %d\n", 10+i, i+1)
	}
	text, _, _ := renderOutline(pageSnapshot{Tree: tree.String()}, outlineOptions{})
	assert.Contains(t, text, "… 4 more checkboxes like these")
}

// Each element a person can act on shows its ID, which an operation names
// it by. A link to the page's own site shows its path, and a list item that
// is one link shows only the link.
func TestOutlineNumbersElementsAndShortensLinks(t *testing.T) {
	t.Parallel()

	tree := `[0-1] RootWebArea: Results
  [0-2] list
    [0-3] listitem
      [0-4] link: Brillia 上野 1億9580万円
        [0-5] StaticText: Brillia 上野 1億9580万円
    [0-6] listitem
      [0-7] link: Elsewhere
        [0-8] StaticText: Elsewhere
  [0-9] button: 検索`
	text, _, _ := renderOutline(pageSnapshot{Tree: tree, URL: "https://suumo.jp/ms/chuko/", URLs: map[string]string{
		"0-4": "https://suumo.jp/ms/chuko/tokyo/nc_1/?x=1",
		"0-7": "https://example.com/other",
	}}, outlineOptions{})
	assert.Equal(t, `list
  [0-4] link "Brillia 上野 1億9580万円" -> /ms/chuko/tokyo/nc_1/?x=1
  [0-7] link "Elsewhere" -> https://example.com/other
[0-9] button "検索"`, text)
}

// A file input shows with its ID and label, by which an upload names it.
func TestOutlineShowsFileInputs(t *testing.T) {
	t.Parallel()

	snap := pageSnapshot{Tree: "[0-1] RootWebArea: Receipts\n  [0-11] LabelText\n    [0-12] StaticText: Receipt\n    [0-13] input, file: Receipt"}
	text, _, _ := renderOutline(snap, outlineOptions{})
	assert.Equal(t, `[0-13] file input "Receipt"`, text)
}

// rowFormAttributes are the name attributes of the controls the rowform
// tree leaves unnamed, as the browser reported them.
var rowFormAttributes = map[string]string{
	"1-9": "KoujiSyubetu", "1-10": "Nendo", "1-11": "KikanNO", "1-12": "AnkenName", "1-103": "Tenpu",
	"1-16": "DateFrom", "1-17": "DateTo", "1-18": "Mojiretu", "1-19": "AndOr", "1-20": "Tantoka",
	"1-13": "FreeWord", "1-14": "lonely", "2-17": "Tanto", "2-18": "Kikan",
}

// A form laid out in a table names a control the tree leaves unnamed by the
// label cell before it in its row, skipping a cell that holds a control or
// a button, in a layout table and through a table nested in a cell too. Controls of one
// role that one cell labels, as the two fields of a date range, are
// numbered in order; a control of another role beside them is not. A
// control no row labels is named by its name or id attribute, and a
// control's own name comes first.
func TestOutlineNamesControlsByTheirRow(t *testing.T) {
	t.Parallel()

	snap := fixtureSnapshot(t, "rowform")
	snap.Attributes = rowFormAttributes
	outline, _, _ := renderOutline(snap, outlineOptions{})
	assert.Equal(t, `iframe
  text: Menu
iframe
  form
    table: 9 rows
      row: 工種又は業種 |
        [1-9] select "工種又は業種" = ; options: , 土木一式工事
      row: 年度 | | 調達機関 | 千葉県
        [1-10] select "年度" = ; options: , 令和8年度
        [1-11] select "調達機関" = 千葉県; options: 千葉県
      row: 案件名 |
        [1-12] textbox "案件名"
      row: 添付あり |
        [1-103] checkbox "添付あり"
      row: 入札予定日範囲 | ～
        [1-16] textbox "入札予定日範囲 1"
        [1-17] textbox "入札予定日範囲 2"
      row: 文字列検索 | AND
        [1-18] textbox "文字列検索"
        [1-19] select "文字列検索" = AND; options: AND, OR
      row: 担当課 | 選択 |
        [1-304] button "選択"
        [1-20] textbox "担当課"
      row: 
        [1-13] textbox "FreeWord"
      row: 入札方式 | 一般競争
        [1-15] checkbox "一般競争"
    [1-14] textbox "lonely"
iframe
  [2-17] textbox "担当者"
  [2-18] select "期間" = 今月; options: 今月`, outline)

	snap.Attributes = nil
	outline, _, _ = renderOutline(snap, outlineOptions{})
	assert.Contains(t, outline, "[1-13] textbox\n", "a control nothing names stays unnamed")
}
