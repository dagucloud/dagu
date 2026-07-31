// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

func TestKeyValue(t *testing.T) {
	t.Parallel()

	t.Run("NewKeyValue", func(t *testing.T) {
		t.Parallel()
		kv := stringutil.NewKeyValue("foo", "bar")
		if kv.String() != "foo=bar" {
			t.Errorf("NewKeyValue() = %v, want %v", kv.String(), "foo=bar")
		}
	})

	t.Run("NewKeyValueWithValueContainingEquals", func(t *testing.T) {
		t.Parallel()
		kv := stringutil.NewKeyValue("key", "value=with=equals")
		if kv.Key() != "key" {
			t.Errorf("Key() = %v, want %v", kv.Key(), "key")
		}
		if kv.Value() != "value=with=equals" {
			t.Errorf("Value() = %v, want %v", kv.Value(), "value=with=equals")
		}
	})

	t.Run("RoundTrip", func(t *testing.T) {
		t.Parallel()
		key := "testkey"
		value := "testvalue"
		kv := stringutil.NewKeyValue(key, value)
		if kv.Key() != key {
			t.Errorf("Round-trip Key() = %v, want %v", kv.Key(), key)
		}
		if kv.Value() != value {
			t.Errorf("Round-trip Value() = %v, want %v", kv.Value(), value)
		}
	})

	tests := []struct {
		name      string
		input     stringutil.KeyValue
		wantKey   string
		wantValue string
		wantBool  bool
	}{
		{
			name:      "NormalKeyValue",
			input:     stringutil.KeyValue("foo=bar"),
			wantKey:   "foo",
			wantValue: "bar",
			wantBool:  false,
		},
		{
			name:      "EmptyValue",
			input:     stringutil.KeyValue("foo="),
			wantKey:   "foo",
			wantValue: "",
			wantBool:  false,
		},
		{
			name:      "EmptyKey",
			input:     stringutil.KeyValue("=bar"),
			wantKey:   "",
			wantValue: "bar",
			wantBool:  false,
		},
		{
			name:      "NoEqualsSign",
			input:     stringutil.KeyValue("foobar"),
			wantKey:   "foobar",
			wantValue: "",
			wantBool:  false,
		},
		{
			name:      "EmptyString",
			input:     stringutil.KeyValue(""),
			wantKey:   "",
			wantValue: "",
			wantBool:  false,
		},
		{
			name:      "BoolTrue",
			input:     stringutil.KeyValue("flag=true"),
			wantKey:   "flag",
			wantValue: "true",
			wantBool:  true,
		},
		{
			name:      "BoolFalse",
			input:     stringutil.KeyValue("flag=false"),
			wantKey:   "flag",
			wantValue: "false",
			wantBool:  false,
		},
		{
			name:      "BoolInvalid",
			input:     stringutil.KeyValue("flag=notbool"),
			wantKey:   "flag",
			wantValue: "notbool",
			wantBool:  false,
		},
		{
			name:      "MultipleEqualsSigns",
			input:     stringutil.KeyValue("foo=bar=baz"),
			wantKey:   "foo",
			wantValue: "bar=baz",
			wantBool:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.input.Key(); got != tt.wantKey {
				t.Errorf("KeyValue.Key() = %v, want %v", got, tt.wantKey)
			}
			if got := tt.input.Value(); got != tt.wantValue {
				t.Errorf("KeyValue.Value() = %v, want %v", got, tt.wantValue)
			}
			if got := tt.input.Bool(); got != tt.wantBool {
				t.Errorf("KeyValue.Bool() = %v, want %v", got, tt.wantBool)
			}
			if got := tt.input.String(); got != string(tt.input) {
				t.Errorf("KeyValue.String() = %v, want %v", got, string(tt.input))
			}
		})
	}
}

func TestKeyValueJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   stringutil.KeyValue
		want    string
		wantErr bool
	}{
		{
			name:    "NormalKeyValue",
			input:   stringutil.KeyValue("foo=bar"),
			want:    `"foo=bar"`,
			wantErr: false,
		},
		{
			name:    "EmptyValue",
			input:   stringutil.KeyValue("foo="),
			want:    `"foo="`,
			wantErr: false,
		},
		{
			name:    "EmptyKey",
			input:   stringutil.KeyValue("=bar"),
			want:    `"=bar"`,
			wantErr: false,
		},
		{
			name:    "EmptyString",
			input:   stringutil.KeyValue(""),
			want:    `""`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Test MarshalJSON
			got, err := json.Marshal(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("KeyValue.MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("KeyValue.MarshalJSON() = %v, want %v", string(got), tt.want)
			}

			// Test UnmarshalJSON
			var kv stringutil.KeyValue
			err = json.Unmarshal(got, &kv)
			if (err != nil) != tt.wantErr {
				t.Errorf("KeyValue.UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if kv != tt.input {
				t.Errorf("KeyValue.UnmarshalJSON() = %v, want %v", kv, tt.input)
			}
		})
	}

	// Test UnmarshalJSON with invalid JSON
	t.Run("InvalidJson", func(t *testing.T) {
		t.Parallel()
		var kv stringutil.KeyValue
		err := json.Unmarshal([]byte(`{invalid`), &kv)
		if err == nil {
			t.Error("KeyValue.UnmarshalJSON() expected error for invalid JSON")
		}
	})
}

func TestKeyValuesToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []string
		want  map[string]string
	}{
		{
			name:  "NormalKeyValues",
			input: []string{"FOO=bar", "BAZ=qux"},
			want: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
		{
			name:  "EmptyValues",
			input: []string{"FOO=", "BAR=value"},
			want: map[string]string{
				"FOO": "",
				"BAR": "value",
			},
		},
		{
			name:  "MultipleEqualsSigns",
			input: []string{"FOO=bar=baz", "KEY=value=with=equals"},
			want: map[string]string{
				"FOO": "bar=baz",
				"KEY": "value=with=equals",
			},
		},
		{
			name:  "NoEqualsSign",
			input: []string{"INVALID", "FOO=bar"},
			want: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name:  "EmptySlice",
			input: []string{},
			want:  map[string]string{},
		},
		{
			name:  "NilSlice",
			input: nil,
			want:  map[string]string{},
		},
		{
			name:  "MixedValidAndInvalid",
			input: []string{"VALID=value", "INVALID", "ANOTHER=test", "=emptykey"},
			want: map[string]string{
				"VALID":   "value",
				"ANOTHER": "test",
				"":        "emptykey",
			},
		},
		{
			name:  "DuplicateKeys",
			input: []string{"FOO=first", "FOO=second"},
			want: map[string]string{
				"FOO": "second", // Last value wins
			},
		},
		{
			name:  "SpecialCharacters",
			input: []string{"PATH=/usr/local/bin", "URL=https://example.com"},
			want: map[string]string{
				"PATH": "/usr/local/bin",
				"URL":  "https://example.com",
			},
		},
		{
			name:  "Whitespace",
			input: []string{"KEY = value", "ANOTHER=  spaces  "},
			want: map[string]string{
				"KEY ":    " value",
				"ANOTHER": "  spaces  ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stringutil.KeyValuesToMap(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("KeyValuesToMap() length = %v, want %v", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("KeyValuesToMap() missing key %q", k)
				} else if gotV != v {
					t.Errorf("KeyValuesToMap()[%q] = %q, want %q", k, gotV, v)
				}
			}
		})
	}
}
