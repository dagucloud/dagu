// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ordersPage is an orders page's tree: a customer's link, a status select,
// a button, a select whose options are not loaded yet, and a custom
// combobox that shows its options only when opened.
var ordersPage = pageSnapshot{
	Tree: "[0-1] RootWebArea: Orders\n  [0-2] link: ACME-1001\n  [0-3] select\n    [0-4] MenuListPopup\n      [0-5] option: すべて [selected]\n      [0-6] option: 未出荷\n  [0-7] button: Save\n" +
		"  [0-8] select\n    [0-9] MenuListPopup\n  [0-10] combobox: Carrier\n",
	XPaths: map[string]string{
		"0-2": "/html[1]/body[1]/a[1]", "0-3": "/html[1]/body[1]/select[1]", "0-7": "/html[1]/body[1]/button[1]",
		"0-8": "/html[1]/body[1]/select[2]", "0-10": "/html[1]/body[1]/input[1]",
	},
}

// rowsPage is a list with a Delete button in each row.
func rowsPage(rows ...string) pageSnapshot {
	snap := pageSnapshot{Tree: "[0-1] RootWebArea: Rows\n  [0-2] list\n", XPaths: map[string]string{}}
	for i, row := range rows {
		button := fmt.Sprintf("3-%d", i)
		snap.Tree += fmt.Sprintf("    [1-%d] listitem\n      [2-%d] StaticText: %s\n      [%s] button: Delete\n", i, i, row, button)
		snap.XPaths[button] = fmt.Sprintf("/html[1]/body[1]/ul[1]/li[%d]/button[1]", i+1)
	}
	return snap
}

// A replay goes ahead only while the element at the recorded location is
// the one the action recorded.
func TestReplayTarget(t *testing.T) {
	t.Parallel()

	customer := map[string]string{"customer": "ACME-1001"}
	for name, tc := range map[string]struct {
		action    recordedAction
		variables map[string]string
		want      bool
	}{
		"same element": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/button[1]", Method: "click", Target: &recordedTarget{Role: "button", Name: "Save"}},
			want:   true,
		},
		"another name": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/button[1]", Method: "click", Target: &recordedTarget{Role: "button", Name: "Delete"}},
		},
		"another role": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/a[1]", Method: "click", Target: &recordedTarget{Role: "button", Name: "ACME-1001"}},
		},
		"element gone": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/button[2]", Method: "click", Target: &recordedTarget{Role: "button", Name: "Save"}},
		},
		"name from a variable": {
			action:    recordedAction{Selector: "xpath=/html[1]/body[1]/a[1]", Method: "click", Target: &recordedTarget{Role: "link", Name: "%customer%"}},
			variables: customer,
			want:      true,
		},
		"name from another value": {
			action:    recordedAction{Selector: "xpath=/html[1]/body[1]/a[1]", Method: "click", Target: &recordedTarget{Role: "link", Name: "%customer%"}},
			variables: map[string]string{"customer": "ACME-1002"},
		},
		"select offering the option": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/select[1]", Method: selectOptionMethod, Arguments: []string{"未出荷"}, Target: &recordedTarget{Role: "select", Lookalikes: 2}},
			want:   true,
		},
		"select lacking the option": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/select[1]", Method: selectOptionMethod, Arguments: []string{"出荷済み"}, Target: &recordedTarget{Role: "select", Lookalikes: 2}},
		},
		"native select without options": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/select[2]", Method: selectOptionMethod, Arguments: []string{"未出荷"}, Target: &recordedTarget{Role: "select", Lookalikes: 2}},
		},
		"custom combobox not showing its options": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/input[1]", Method: selectOptionMethod, Arguments: []string{"Express"}, Target: &recordedTarget{Role: "combobox", Name: "Carrier"}},
			want:   true,
		},
		"unidentified select lacking the option": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/select[1]", Method: selectOptionMethod, Arguments: []string{"出荷済み"}},
		},
		// A recording made before actions were identified replays as before.
		"unidentified element": {
			action: recordedAction{Selector: "xpath=/html[1]/body[1]/button[2]", Method: "click"},
			want:   true,
		},
	} {
		assert.Equal(t, tc.want, stillTargets(tc.action, ordersPage, tc.variables), name)
	}
}

// An action is identified by the element it targets, with a variable's
// value in the name kept as its placeholder.
func TestIdentifyActions(t *testing.T) {
	t.Parallel()

	actions := []recordedAction{
		{Selector: "xpath=/html[1]/body[1]/a[1]", Method: "click"},
		{Selector: "xpath=/html[1]/body[1]/div[1]", Method: "click"},
	}
	identifyOn(ordersPage, actions, map[string]string{"customer": "ACME-1001", "short": "AC"})

	require.Len(t, actions, 2)
	require.NotNil(t, actions[0].Target)
	assert.Equal(t, recordedTarget{Role: "link", Name: "%customer%"}, *actions[0].Target)
	assert.Nil(t, actions[1].Target, "an element outside the tree is not identified")
	assert.True(t, stillTargets(actions[0], ordersPage, map[string]string{"customer": "ACME-1001"}))
}

// An element with lookalikes on the page, such as a Delete button in each
// row, is told apart by the text of its row, which may show a variable.
func TestReplayTargetInRow(t *testing.T) {
	t.Parallel()

	actions := []recordedAction{{Selector: "xpath=/html[1]/body[1]/ul[1]/li[2]/button[1]", Method: "click"}}
	identifyOn(rowsPage("Alpha", "Beta"), actions, map[string]string{"customer": "Beta"})
	deleteCustomer := actions[0]

	assert.True(t, stillTargets(deleteCustomer, rowsPage("Alpha", "Beta"), map[string]string{"customer": "Beta"}))
	assert.True(t, stillTargets(deleteCustomer, rowsPage("Alpha", "Delta"), map[string]string{"customer": "Delta"}), "the row of the customer this run names")
	assert.False(t, stillTargets(deleteCustomer, rowsPage("Gamma", "Alpha", "Beta"), map[string]string{"customer": "Beta"}), "Alpha's Delete is where Beta's was")

	// Rows that differ only after a long shared prefix are told apart too.
	prefix := strings.Repeat("Shared warehouse note ", 20)
	long := []recordedAction{{Selector: "xpath=/html[1]/body[1]/ul[1]/li[2]/button[1]", Method: "click"}}
	identifyOn(rowsPage(prefix+"A-1", prefix+"A-2"), long, nil)
	assert.False(t, stillTargets(long[0], rowsPage(prefix+"A-0", prefix+"A-1", prefix+"A-2"), nil))

	// A lone element has nothing to be told apart from, so it is identified
	// by its role and name, until lookalikes appear beside it.
	lone := []recordedAction{{Selector: "xpath=/html[1]/body[1]/ul[1]/li[1]/button[1]", Method: "click"}}
	identifyOn(rowsPage("Alpha"), lone, nil)
	assert.True(t, stillTargets(lone[0], rowsPage("Beta"), nil))
	assert.False(t, stillTargets(lone[0], rowsPage("New", "Alpha"), nil), "New's Delete is where Alpha's was")
}

// A recording may name no control where the outline now names one by its
// table row, as recordings made before rows named controls do. Such a
// recording still finds the control, compared by the names the tree gives.
// The empty name is no wildcard: a control with a name of its own at that
// place is another element.
func TestReplayTargetRecordedUnnamed(t *testing.T) {
	t.Parallel()

	snap := fixtureSnapshot(t, "rowform")
	snap.Attributes = rowFormAttributes
	snap.XPaths = map[string]string{
		"1-9":  "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[1]/td[1]/select[1]",
		"1-13": "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[8]/td[1]/input[1]",
		"1-15": "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[9]/td[2]/input[1]",
	}
	pick := recordedAction{Selector: "xpath=" + snap.XPaths["1-9"], Method: selectOptionMethod, Arguments: []string{"土木一式工事"}}
	unnamed := parseSnapshotTree(snap.Tree)
	recorded := identified(pick, pageTarget{roots: unnamed, path: pathTo(unnamed, "1-9")}, nil)
	require.Empty(t, recorded.Target.Name)
	assert.True(t, stillTargets(recorded, snap, nil), "a select recorded before rows named it")

	// A control nothing names is recorded and replayed alike.
	bare := snap
	bare.Attributes = nil
	actions := []recordedAction{{Selector: "xpath=" + snap.XPaths["1-13"], Method: "fill", Arguments: []string{"舗装"}}}
	identifyOn(bare, actions, nil)
	require.NotNil(t, actions[0].Target)
	assert.Empty(t, actions[0].Target.Name)
	assert.True(t, stillTargets(actions[0], bare, nil), "a field nothing names")

	own := recordedAction{Selector: "xpath=" + snap.XPaths["1-15"], Method: "click", Target: &recordedTarget{Role: "checkbox"}}
	assert.False(t, stillTargets(own, snap, nil), "a checkbox with a name of its own")
}

// An act picking an option does not count as done unless the page before
// it shows the select offers the option. The reason names the option
// compared, filled in with its variables' values unless one is secret, and
// the options the select offers.
func TestUncheckedOption(t *testing.T) {
	t.Parallel()

	pick := func(selector, option string) []recordedAction {
		return []recordedAction{{Selector: selector, Method: selectOptionMethod, Arguments: []string{option}}}
	}
	status := "xpath=/html[1]/body[1]/select[1]"
	assert.Empty(t, uncheckedOption(ordersPage, nil, pick(status, "未出荷"), nil, nil))
	assert.Equal(t, `the select offers no option "出荷済み"; it offers "すべて", "未出荷"`,
		uncheckedOption(ordersPage, nil, pick(status, "出荷済み"), nil, nil))
	assert.Equal(t, `the select offers no option "出荷済み"; it has no options`,
		uncheckedOption(ordersPage, nil, pick("xpath=/html[1]/body[1]/select[2]", "出荷済み"), nil, nil))
	assert.Contains(t, uncheckedOption(pageSnapshot{}, errors.New("page busy"), pick(status, "未出荷"), nil, nil), "page busy")
	assert.Empty(t, uncheckedOption(pageSnapshot{}, errors.New("page busy"), []recordedAction{{Selector: status, Method: "click"}}, nil, nil),
		"only a picked option needs the page")

	// A reference that stayed literal leaves the variable holding text the
	// select cannot offer.
	variables := map[string]string{"state": "${item.state}", "code": "hunter2"}
	isCode := func(name string) bool { return name == "code" }
	assert.Equal(t, `the select offers no option "${item.state}", the value of "%state%"; it offers "すべて", "未出荷"`,
		uncheckedOption(ordersPage, nil, pick(status, "%state%"), variables, isCode))
	assert.Equal(t, `the select offers no option "${item.state} 発送", the value of "%state% 発送"; it offers "すべて", "未出荷"`,
		uncheckedOption(ordersPage, nil, pick(status, "%state% 発送"), variables, isCode))
	assert.Equal(t, `the select offers no option for the secret value of "%state% %code%"; it offers "すべて", "未出荷"`,
		uncheckedOption(ordersPage, nil, pick(status, "%state% %code%"), variables, isCode))
	assert.Equal(t, `the select offers no option for the secret value of "%state%"; it offers "すべて", "未出荷"`,
		uncheckedOption(ordersPage, nil, pick(status, "%state%"), variables, nil), "a nil secret counts every value as one")
}

// A select's options are listed up to the number the outline shows.
func TestUncheckedOptionListsFirstOptions(t *testing.T) {
	t.Parallel()

	snap := pageSnapshot{Tree: "[0-1] RootWebArea: Form\n  [0-2] combobox: Prefecture\n", XPaths: map[string]string{"0-2": "/html[1]/body[1]/input[1]"}}
	var offered []string
	for i := range outlineOptionCount + 2 {
		snap.Tree += fmt.Sprintf("    [1-%d] option: P%d\n", i, i)
		if i < outlineOptionCount {
			offered = append(offered, fmt.Sprintf(`"P%d"`, i))
		}
	}
	picked := []recordedAction{{Selector: "xpath=/html[1]/body[1]/input[1]", Method: selectOptionMethod, Arguments: []string{"Q"}}}
	assert.Equal(t, `the combobox "Prefecture" offers no option "Q"; it offers `+strings.Join(offered, ", ")+" (+2 more)",
		uncheckedOption(snap, nil, picked, nil, nil))
}

// gridPage is a table of unlabeled inputs under the given column headers,
// one row per line number.
func gridPage(headers ...string) pageSnapshot {
	snap := pageSnapshot{Tree: "[0-1] RootWebArea: Order\n  [0-2] table\n    [0-3] row\n", XPaths: map[string]string{}}
	snap.Tree += "      [h-n] columnheader: Line\n"
	for i, header := range headers {
		snap.Tree += fmt.Sprintf("      [h-%d] columnheader: %s\n", i, header)
	}
	snap.Tree += "    [0-4] row\n      [c-0] cell\n        [t-0] StaticText: 1\n"
	for i := range headers {
		snap.Tree += fmt.Sprintf("      [c-%d] cell\n        [i-%d] textbox\n", i+1, i)
		snap.XPaths[fmt.Sprintf("i-%d", i)] = fmt.Sprintf("/html[1]/body[1]/table[1]/tr[2]/td[%d]/input[1]", i+2)
	}
	return snap
}

// An unlabeled input is told by the header of its column, so swapped
// columns do not match.
func TestReplayTargetColumn(t *testing.T) {
	t.Parallel()

	actions := []recordedAction{{Selector: "xpath=/html[1]/body[1]/table[1]/tr[2]/td[2]/input[1]", Method: "fill"}}
	identifyOn(gridPage("item code", "quantity"), actions, nil)

	assert.Equal(t, "item code", actions[0].Target.Column)
	assert.True(t, stillTargets(actions[0], gridPage("item code", "quantity"), nil))
	assert.False(t, stillTargets(actions[0], gridPage("quantity", "item code"), nil))
}

// A table without a header row gives its inputs no column, even when
// another table on the page has one.
func TestReplayTargetColumnOwnTable(t *testing.T) {
	t.Parallel()

	snap := gridPage("item code", "quantity")
	snap.Tree = strings.Replace(snap.Tree, "  [0-2] table\n", "  [o-1] table\n    [o-2] row\n      [o-3] columnheader: Other\n      [o-4] columnheader: Total\n  [0-2] table\n", 1)
	snap.Tree = strings.NewReplacer("columnheader: Line", "cell: Line", "columnheader: item code", "cell: item code", "columnheader: quantity", "cell: quantity").Replace(snap.Tree)
	actions := []recordedAction{{Selector: "xpath=/html[1]/body[1]/table[1]/tr[2]/td[2]/input[1]", Method: "fill"}}
	identifyOn(snap, actions, nil)

	assert.Empty(t, actions[0].Target.Column)
}

// A variable that only a column header holds is still used by the recording.
func TestUnusedVariablesCountsColumn(t *testing.T) {
	t.Parallel()

	actions := []recordedAction{{Target: &recordedTarget{Role: "textbox", Column: "%column%"}}}
	assert.Empty(t, unusedVariables("Fill the %column% field", actions))
}
