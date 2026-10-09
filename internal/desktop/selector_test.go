// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"encoding/json"
	"errors"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// formWindow and formElements mirror the conformance window in
// conformance/spec074_computer/testdata/elements.ps1, listed in the
// reverse order WinForms exposes children, so reading order matters.
var formWindow = Element{Role: RoleWindow, Name: "Dagu elements test", App: "powershell", Bounds: image.Rect(100, 100, 700, 500), Window: "Dagu elements test"}

func formElements() []Element {
	group := []PathStep{{Role: RoleGroup, Name: "支払情報", Index: 0}}
	return []Element{
		{Role: RoleGroup, Name: "支払情報", ID: "paymentGroup", Bounds: image.Rect(120, 300, 660, 450), Path: group},
		{Role: RoleButton, Name: "保存", ID: "paymentSaveButton", Bounds: image.Rect(210, 370, 300, 398), Path: append(group[:1:1], PathStep{Role: RoleButton, Name: "保存", Index: 0})},
		{Role: RoleTextField, ID: "paymentAmountBox", Bounds: image.Rect(210, 330, 410, 354), Path: append(group[:1:1], PathStep{Role: RoleTextField, Index: 0})},
		{Role: RoleText, Name: "金額", ID: "lblPaymentAmount", Bounds: image.Rect(140, 330, 200, 354), Path: append(group[:1:1], PathStep{Role: RoleText, Name: "金額", Index: 0})},
		{Role: RoleComboBox, Name: "支払方法", ID: "methodBox", Bounds: image.Rect(190, 250, 390, 274), Path: []PathStep{{Role: RoleComboBox, Name: "支払方法", Index: 0}}},
		{Role: RoleCheckbox, Name: "同意する", ID: "agreeBox", Bounds: image.Rect(190, 210, 300, 234), Path: []PathStep{{Role: RoleCheckbox, Name: "同意する", Index: 0}}},
		{Role: RoleButton, Name: "保存", ID: "saveButton", Bounds: image.Rect(190, 170, 280, 198), Path: []PathStep{{Role: RoleButton, Name: "保存", Index: 0}}},
		{Role: RoleTextField, ID: "amountBox", Bounds: image.Rect(190, 130, 390, 154), Path: []PathStep{{Role: RoleTextField, Index: 0}}},
		{Role: RoleText, Name: "金額", ID: "lblAmount", Bounds: image.Rect(120, 130, 180, 154), Path: []PathStep{{Role: RoleText, Name: "金額", Index: 0}}},
	}
}

func ids(elements []Element) []string {
	var out []string
	for _, e := range elements {
		out = append(out, e.ID)
	}
	return out
}

func TestSelectorValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		sel  Selector
		want string
	}{
		{"name", Selector{Role: RoleButton, Name: "保存"}, ""},
		{"id", Selector{Role: RoleButton, ID: "save"}, ""},
		{"near", Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}}, ""},
		{"nested", Selector{Role: RoleButton, Name: "保存", In: &Selector{Role: RoleGroup, Name: "支払情報"}}, ""},
		{"no role", Selector{Name: "保存"}, "role is required"},
		{"unknown role", Selector{Role: "knob", Name: "x"}, `unknown role "knob"`},
		{"role alone", Selector{Role: RoleButton}, "set name, id, or near"},
		{"near without label", Selector{Role: RoleButton, Near: &Near{Side: SideRight}}, "near.label is required"},
		{"bad side", Selector{Role: RoleButton, Near: &Near{Label: "x", Side: "beside"}}, "near.side must be one of"},
		{"negative nth", Selector{Role: RoleButton, Name: "x", Nth: new(-1)}, "nth must be 0 or more"},
		{"bad container", Selector{Role: RoleButton, Name: "x", In: &Selector{Role: RoleGroup}}, "selector.in: set name, id, or near"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.sel.Validate()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSelectorJSON(t *testing.T) {
	t.Parallel()

	const text = `{"app":"Expense","window":"経費精算","role":"text_field","name":"請求書 %number%*","id":"amount","in":{"role":"group","name":"支払情報"},"near":{"label":"金額","side":"right"},"nth":0}`
	sel, err := ParseSelector([]byte(text))
	require.NoError(t, err)
	assert.Equal(t, "請求書 %number%*", sel.Name, "placeholders are kept as written")
	require.NotNil(t, sel.Nth)
	assert.Equal(t, 0, *sel.Nth, "nth 0 is kept")
	assert.Equal(t, text, sel.String(), "String is the compact JSON form")

	data, err := json.Marshal(sel)
	require.NoError(t, err)
	assert.JSONEq(t, text, string(data))

	_, err = ParseSelector([]byte(`{"role":"button","title":"保存"}`))
	require.ErrorContains(t, err, "title")
	_, err = ParseSelector([]byte(`{"role":"button"}`))
	require.ErrorContains(t, err, "set name, id, or near")
	_, err = ParseSelector([]byte(`{`))
	require.ErrorContains(t, err, "selector:")
}

func TestMatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		sel  Selector
		want []string
		err  string
	}{
		{name: "name twice", sel: Selector{Role: RoleButton, Name: "保存"}, want: []string{"saveButton", "paymentSaveButton"}},
		{name: "wildcard", sel: Selector{Role: RoleCheckbox, Name: "同意*"}, want: []string{"agreeBox"}},
		{name: "wrong role", sel: Selector{Role: RoleLink, Name: "保存"}, want: nil},
		{name: "exact name", sel: Selector{Role: RoleCheckbox, Name: "同意"}, want: nil},
		{name: "id", sel: Selector{Role: RoleButton, ID: "paymentSaveButton"}, want: []string{"paymentSaveButton"}},
		{name: "wrong id does not fall back to name", sel: Selector{Role: RoleButton, ID: "nope", Name: "保存"}, want: nil},
		{name: "in", sel: Selector{Role: RoleButton, Name: "保存", In: &Selector{Role: RoleGroup, Name: "支払情報"}}, want: []string{"paymentSaveButton"}},
		{name: "in by id", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}, In: &Selector{Role: RoleGroup, ID: "paymentGroup"}}, want: []string{"paymentAmountBox"}},
		{name: "container missing", sel: Selector{Role: RoleButton, Name: "保存", In: &Selector{Role: RoleGroup, Name: "請求"}}, err: `container {"role":"group","name":"請求"}: no element matches`},
		{name: "container ambiguous", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}, In: &Selector{Role: RoleText, Name: "金額"}}, err: `container {"role":"text","name":"金額"}: 2 elements match`},
		{name: "near twice", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}}, want: []string{"amountBox", "paymentAmountBox"}},
		{name: "near and nth", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}, Nth: new(0)}, want: []string{"amountBox"}},
		{name: "near wrong side", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideBelow}}, want: nil},
		{name: "near above", sel: Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideAbove}}, want: []string{"amountBox"}},
		{name: "nth", sel: Selector{Role: RoleButton, Name: "保存", Nth: new(1)}, want: []string{"paymentSaveButton"}},
		{name: "nth past the end", sel: Selector{Role: RoleButton, Name: "保存", Nth: new(2)}, want: nil},
		{name: "window contains", sel: Selector{Window: "elements", Role: RoleComboBox, Name: "支払方法"}, want: []string{"methodBox"}},
		{name: "window wildcard", sel: Selector{Window: "Dagu*test", Role: RoleComboBox, Name: "支払方法"}, want: []string{"methodBox"}},
		{name: "other window", sel: Selector{Window: "経費精算", Role: RoleComboBox, Name: "支払方法"}, want: nil},
		{name: "app ignores case", sel: Selector{App: "PowerShell", Role: RoleComboBox, Name: "支払方法"}, want: []string{"methodBox"}},
		{name: "other app", sel: Selector{App: "Expense", Role: RoleComboBox, Name: "支払方法"}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matches, err := Match(formWindow, formElements(), tc.sel)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, ids(matches))
		})
	}
}

// The declared label relation wins over geometry.
func TestMatchNearRelation(t *testing.T) {
	t.Parallel()

	elements := formElements()
	for i := range elements {
		if elements[i].ID == "paymentAmountBox" {
			elements[i].Label = "合計"
		}
	}
	matches, err := Match(formWindow, elements, Selector{Role: RoleTextField, Near: &Near{Label: "合計", Side: SideLeft}})
	require.NoError(t, err)
	assert.Equal(t, []string{"paymentAmountBox"}, ids(matches), "the side is not checked when the application declares the label")
}

func TestOne(t *testing.T) {
	t.Parallel()

	sel := Selector{Role: RoleButton, Name: "保存"}
	_, err := One(nil, sel)
	require.ErrorIs(t, err, ErrNotFound)
	assert.EqualError(t, err, `no element matches {"role":"button","name":"保存"}`)

	one, err := One(formElements()[:1], sel)
	require.NoError(t, err)
	assert.Equal(t, "paymentGroup", one.ID)

	_, err = One(formElements()[:2], sel)
	require.ErrorIs(t, err, ErrAmbiguous)
	var ambiguous *AmbiguousError
	require.True(t, errors.As(err, &ambiguous))
	assert.Equal(t, 2, ambiguous.Count)
	assert.EqualError(t, err, `2 elements match {"role":"button","name":"保存"}; add in, near, or nth`)
}

func TestReadingOrder(t *testing.T) {
	t.Parallel()

	ordered := readingOrder(formElements())
	assert.Equal(t, []string{
		"lblAmount", "amountBox", "saveButton", "agreeBox", "methodBox",
		"paymentGroup", "lblPaymentAmount", "paymentAmountBox", "paymentSaveButton",
	}, ids(ordered))

	// A label a few pixels higher than its field shares the field's row.
	label := Element{ID: "label", Bounds: image.Rect(10, 8, 60, 28)}
	field := Element{ID: "field", Bounds: image.Rect(70, 12, 200, 36)}
	next := Element{ID: "next", Bounds: image.Rect(10, 40, 60, 60)}
	assert.Equal(t, []string{"label", "field", "next"}, ids(readingOrder([]Element{next, field, label})))
}
