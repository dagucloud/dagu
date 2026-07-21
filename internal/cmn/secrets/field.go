// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func selectJSONField(value, field string) (string, error) {
	if field == "" {
		return value, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
		return "", fmt.Errorf("secret value is not a JSON object")
	}

	raw, ok := object[field]
	if !ok {
		return "", fmt.Errorf("field %q was not found in secret value", field)
	}

	var text *string
	if err := json.Unmarshal(raw, &text); err == nil && text != nil {
		return *text, nil
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", fmt.Errorf("field %q contains invalid JSON", field)
	}
	return compact.String(), nil
}
