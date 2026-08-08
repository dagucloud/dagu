// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectJSONField(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		field   string
		want    string
		wantErr string
	}{
		{name: "RawValue", value: "plain-text", want: "plain-text"},
		{name: "String", value: `{"token":"secret"}`, field: "token", want: "secret"},
		{name: "Number", value: `{"port":5432}`, field: "port", want: "5432"},
		{name: "LargeNumber", value: `{"id":9007199254740993}`, field: "id", want: "9007199254740993"},
		{name: "Object", value: `{"database":{"host":"db","port":5432}}`, field: "database", want: `{"host":"db","port":5432}`},
		{name: "ObjectWithLargeNumber", value: `{"record":{"id":9007199254740993}}`, field: "record", want: `{"id":9007199254740993}`},
		{name: "Null", value: `{"token":null}`, field: "token", want: "null"},
		{name: "InvalidJSON", value: "not-json", field: "token", wantErr: "not a JSON object"},
		{name: "Array", value: `["secret"]`, field: "token", wantErr: "not a JSON object"},
		{name: "NullObject", value: `null`, field: "token", wantErr: "not a JSON object"},
		{name: "MissingField", value: `{"token":"secret"}`, field: "password", wantErr: `field "password" was not found`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectJSONField(tc.value, tc.field)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), tc.value)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
