// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepeatPolicy_UnmarshalJSON(t *testing.T) {
	t.Run("LegacyBooleanTrue", func(t *testing.T) {
		// Test legacy boolean true - should convert to "while" mode
		jsonData := `{
			"repeat": true,
			"interval": 60000000000
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, 60*time.Second, rp.Interval)
	})

	t.Run("LegacyBooleanFalseWithCondition", func(t *testing.T) {
		// Test legacy boolean false with condition - should default to "until"
		jsonData := `{
			"repeat": false,
			"condition": {
				"condition": "test -f /tmp/file",
				"expected": "true"
			}
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeUntil, rp.RepeatMode)
		assert.NotNil(t, rp.Condition)
	})

	t.Run("LegacyBooleanFalseWithExitCode", func(t *testing.T) {
		// Test legacy boolean false with exit code - should default to "while"
		jsonData := `{
			"repeat": false,
			"exitCode": [0, 1],
			"interval": 30000000000
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, []int{0, 1}, rp.ExitCode)
		assert.Equal(t, 30*time.Second, rp.Interval)
	})

	t.Run("LegacyBooleanFalseNoConditionOrExitCode", func(t *testing.T) {
		// Test legacy boolean false with no condition or exit code - should leave RepeatMode empty (no repeat)
		jsonData := `{
			"repeat": false
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatMode(""), rp.RepeatMode) // No repeat mode set
	})

	t.Run("NewRepeatModeWhile", func(t *testing.T) {
		// Test new repeat mode "while"
		jsonData := `{
			"repeatMode": "while",
			"condition": {
				"condition": "echo test"
			},
			"interval": 45000000000
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.NotNil(t, rp.Condition)
		assert.Equal(t, 45*time.Second, rp.Interval)
	})

	t.Run("NewRepeatModeUntil", func(t *testing.T) {
		// Test new repeat mode "until"
		jsonData := `{
			"repeatMode": "until",
			"exitCode": [0],
			"limit": 5
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeUntil, rp.RepeatMode)
		assert.Equal(t, []int{0}, rp.ExitCode)
		assert.Equal(t, 5, rp.Limit)
	})

	t.Run("LegacyStatusDataFromFile", func(t *testing.T) {
		// Test real-world scenario: unmarshaling status data that was saved before the enhancement
		// This would have been saved with repeat: true/false format
		jsonData := `{
			"repeat": true,
			"interval": 30000000000,
			"condition": {
				"condition": "check_status.sh",
				"expected": ""
			}
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		// Legacy repeat: true with condition (no expected) should become "while"
		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, 30*time.Second, rp.Interval)
		assert.NotNil(t, rp.Condition)
		assert.Equal(t, "check_status.sh", rp.Condition.Condition)
		assert.Equal(t, "", rp.Condition.Expected)
	})

	t.Run("EmptyRepeatPolicy", func(t *testing.T) {
		// Test empty repeat policy
		jsonData := `{}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatMode(""), rp.RepeatMode)
		assert.Nil(t, rp.Condition)
		assert.Empty(t, rp.ExitCode)
		assert.Equal(t, time.Duration(0), rp.Interval)
	})

	t.Run("CompleteRepeatPolicy", func(t *testing.T) {
		// Test complete repeat policy with all fields
		jsonData := `{
			"repeatMode": "while",
			"interval": 120000000000,
			"limit": 10,
			"condition": {
				"condition": "check_status",
				"expected": "running"
			},
			"exitCode": [1, 2, 3]
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(jsonData), &rp)
		require.NoError(t, err)

		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, 120*time.Second, rp.Interval)
		assert.Equal(t, 10, rp.Limit)
		assert.NotNil(t, rp.Condition)
		assert.Equal(t, "check_status", rp.Condition.Condition)
		assert.Equal(t, "running", rp.Condition.Expected)
		assert.Equal(t, []int{1, 2, 3}, rp.ExitCode)
	})
}

func TestRepeatPolicy_MarshalUnmarshal(t *testing.T) {
	t.Run("CurrentFormatMarshalUnmarshal", func(t *testing.T) {
		// Test that current format can be marshaled and unmarshaled correctly
		original := RepeatPolicy{
			RepeatMode: RepeatModeWhile,
			Interval:   30 * time.Second,
			Limit:      5,
			Condition: &Condition{
				Condition: "test condition",
				Expected:  "expected",
			},
		}

		// Marshal
		data, err := json.Marshal(original)
		require.NoError(t, err)

		// Check the JSON structure
		var jsonMap map[string]any
		err = json.Unmarshal(data, &jsonMap)
		require.NoError(t, err)

		// Should have repeatMode, not repeat
		assert.Contains(t, jsonMap, "repeatMode")
		assert.Equal(t, "while", jsonMap["repeatMode"])
		assert.NotContains(t, jsonMap, "repeat")

		// Unmarshal back
		var unmarshaled RepeatPolicy
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, original.RepeatMode, unmarshaled.RepeatMode)
		assert.Equal(t, original.Interval, unmarshaled.Interval)
		assert.Equal(t, original.Limit, unmarshaled.Limit)
		assert.Equal(t, original.Condition.Condition, unmarshaled.Condition.Condition)
		assert.Equal(t, original.Condition.Expected, unmarshaled.Condition.Expected)
	})

	t.Run("LegacyFormatCompatibility", func(t *testing.T) {
		// Simulate legacy status data that would have been saved before the enhancement
		// This tests the actual backward compatibility need
		legacyData := `{
			"repeat": true,
			"interval": 60000000000,
			"condition": {
				"condition": "check_status",
				"expected": ""
			}
		}`

		var rp RepeatPolicy
		err := json.Unmarshal([]byte(legacyData), &rp)
		require.NoError(t, err)

		// Should convert legacy repeat:true to RepeatModeWhile
		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, 60*time.Second, rp.Interval)
		assert.NotNil(t, rp.Condition)
		assert.Equal(t, "check_status", rp.Condition.Condition)
		assert.Equal(t, "", rp.Condition.Expected)
	})

	t.Run("RoundTripWithLegacyData", func(t *testing.T) {
		// Test that we can read legacy data and write it in new format
		legacyData := `{
			"repeat": true,
			"interval": 30000000000,
			"exitCode": [1, 2]
		}`

		// Unmarshal legacy data
		var rp RepeatPolicy
		err := json.Unmarshal([]byte(legacyData), &rp)
		require.NoError(t, err)

		// Should have converted to new format
		assert.Equal(t, RepeatModeWhile, rp.RepeatMode)
		assert.Equal(t, 30*time.Second, rp.Interval)
		assert.Equal(t, []int{1, 2}, rp.ExitCode)

		// Marshal to new format
		newData, err := json.Marshal(rp)
		require.NoError(t, err)

		// Check new format
		var jsonMap map[string]any
		err = json.Unmarshal(newData, &jsonMap)
		require.NoError(t, err)

		// Should now have repeatMode instead of repeat
		assert.Contains(t, jsonMap, "repeatMode")
		assert.Equal(t, "while", jsonMap["repeatMode"])
		assert.NotContains(t, jsonMap, "repeat")

		// Can unmarshal the new format
		var rp2 RepeatPolicy
		err = json.Unmarshal(newData, &rp2)
		require.NoError(t, err)
		assert.Equal(t, rp.RepeatMode, rp2.RepeatMode)
	})
}

func TestRepeatPolicy_StrFieldsRoundTrip(t *testing.T) {
	original := RepeatPolicy{
		RepeatMode:     RepeatModeWhile,
		LimitStr:       "${max_rounds}",
		IntervalStr:    "$INTERVAL",
		MaxIntervalStr: "${MAX_INTERVAL}",
		Condition: &Condition{
			Condition: "true",
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var unmarshaled RepeatPolicy
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, original.LimitStr, unmarshaled.LimitStr)
	assert.Equal(t, original.IntervalStr, unmarshaled.IntervalStr)
	assert.Equal(t, original.MaxIntervalStr, unmarshaled.MaxIntervalStr)
	assert.Equal(t, original.RepeatMode, unmarshaled.RepeatMode)
}

func TestCommandEntry_String(t *testing.T) {
	tests := []struct {
		name     string
		entry    CommandEntry
		expected string
	}{
		{
			name:     "Empty",
			entry:    CommandEntry{},
			expected: "",
		},
		{
			name:     "CommandOnly",
			entry:    CommandEntry{Command: "echo"},
			expected: "echo",
		},
		{
			name:     "CommandWithArgs",
			entry:    CommandEntry{Command: "echo", Args: []string{"hello", "world"}},
			expected: "echo hello world",
		},
		{
			name:     "CmdWithArgsTakesPriority",
			entry:    CommandEntry{Command: "echo", Args: []string{"hello"}, CmdWithArgs: "echo 'hello world'"},
			expected: "echo 'hello world'",
		},
		{
			name:     "CmdWithArgsOnly",
			entry:    CommandEntry{CmdWithArgs: "ls -la"},
			expected: "ls -la",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.entry.String())
		})
	}
}

func TestStepMarshalPreservesExplicitEmptyOutputSchema(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Step{OutputSchema: map[string]any{}})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"outputSchema":{}`)

	var step Step
	require.NoError(t, json.Unmarshal(data, &step))
	assert.True(t, step.HasOutputSchema())
}

func TestStepUnmarshalJSONCopiesLegacyArgs(t *testing.T) {
	t.Parallel()

	var step Step
	err := json.Unmarshal([]byte(`{"command":"echo","args":["hello"],"cmdWithArgs":"echo hello"}`), &step)
	require.NoError(t, err)
	require.Len(t, step.Commands, 1)
	require.Equal(t, []string{"hello"}, step.Commands[0].Args)

	step.Args[0] = "changed"
	assert.Equal(t, []string{"hello"}, step.Commands[0].Args)
}
