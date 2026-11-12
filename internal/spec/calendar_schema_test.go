// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"encoding/json"
	"testing"

	cmnschema "github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/goccy/go-yaml"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The editor validates calendar files against the schema before the server
// parses them, so the two must agree. Only the parser can reject dates that
// do not exist, ranges, and includes, which the schema cannot see.
func TestCalendarSchemaAgreesWithParser(t *testing.T) {
	t.Parallel()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(cmnschema.CalendarSchemaJSON, &schema))
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	source := spec.NewCalendarSource(writeCalendars(t, map[string]string{"jp-national": jpNationalCalendar}))

	for _, tc := range []struct {
		name    string
		content string
		valid   bool
		// parserOnly marks a document only the parser rejects.
		parserOnly bool
	}{
		{name: "every key", content: jpBankingCalendar, valid: true},
		{name: "weekdays in any case", content: "weekdays: [mon, Tue, SAT]\n", valid: true},
		{name: "template", content: string(spec.CalendarTemplate(2027)), valid: true},
		{name: "unknown key", content: "holiday: [2027-01-01]\n"},
		{name: "description not text", content: "description: [a]\n"},
		{name: "empty weekdays", content: "weekdays: []\n"},
		{name: "unknown weekday", content: "weekdays: [MON, FUN]\n"},
		{name: "holidays not a list", content: "holidays: 2027-01-01\n"},
		{name: "short date", content: "holidays: [2027-1-1]\n"},
		{name: "holiday not a date", content: "holidays: [1]\n"},
		{name: "date and range", content: "holidays:\n  - {date: 2027-01-05, from: 2027-01-05, to: 2027-01-06}\n"},
		{name: "half a range", content: "holidays:\n  - {from: 2027-01-05}\n"},
		{name: "unknown holiday key", content: "holidays:\n  - {date: 2027-01-05, nam: x}\n"},
		{name: "holiday name not text", content: "holidays:\n  - {date: 2027-01-05, name: [x]}\n"},
		{name: "extra business day not a date", content: "extra_business_days: [soon]\n"},
		{name: "include not a name", content: "include: [1]\n"},
		{name: "include outside the directory", content: "include: [../jp-national]\n"},
		{name: "through not a date", content: "through: someday\n"},
		{name: "date that does not exist", content: "holidays: [2027-02-30]\n", parserOnly: true},
		{name: "range ends before it starts", content: "holidays:\n  - {from: 2027-01-05, to: 2027-01-03}\n", parserOnly: true},
		{name: "range over 366 days", content: "holidays:\n  - {from: 2027-01-01, to: 2028-01-02}\n", parserOnly: true},
		{name: "missing include", content: "include: [missing]\n", parserOnly: true},
		{name: "include cycle", content: "include: [draft]\n", parserOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var document any
			require.NoError(t, yaml.Unmarshal([]byte(tc.content), &document))
			schemaErr := resolved.Validate(document)
			_, errs := source.Check("draft", []byte(tc.content))

			assert.Equal(t, tc.valid, len(errs) == 0, "parser: %v", errs)
			assert.Equal(t, tc.valid || tc.parserOnly, schemaErr == nil, "schema: %v", schemaErr)
		})
	}
}
