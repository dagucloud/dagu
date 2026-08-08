// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"fmt"
)

// ParamType identifies which field in Params is active
type ParamType int

const (
	ParamTypeUnknown ParamType = iota
	ParamTypeString            // Simple map[string]string
	ParamTypeAny               // Rich map[string]any
	ParamTypeRaw               // Lazy json.RawMessage
)

// String returns the string representation of ParamType
func (t ParamType) String() string {
	switch t {
	case ParamTypeString:
		return "string"
	case ParamTypeAny:
		return "any"
	case ParamTypeRaw:
		return "raw"
	case ParamTypeUnknown:
		fallthrough
	default:
		return "unknown"
	}
}

// Params holds parameter data in one of several formats
// Only one of Simple, Rich, or Raw should be non-nil
type Params struct {
	// Simple params (backward compatible)
	Simple map[string]string `json:"simple,omitempty"`

	// Rich params with type preservation
	Rich map[string]any `json:"rich,omitempty"`

	// Raw params for lazy parsing
	Raw json.RawMessage `json:"raw,omitempty"`
}

// NewSimpleParams creates params from map[string]string
func NewSimpleParams(data map[string]string) Params {
	return Params{Simple: data}
}

// NewRichParams creates params from map[string]any
func NewRichParams(data map[string]any) Params {
	return Params{Rich: data}
}

// NewRawParams creates params from json.RawMessage
func NewRawParams(raw json.RawMessage) Params {
	return Params{Raw: raw}
}

// ParseParams creates Params from any input type
func ParseParams(input any) (Params, error) {
	if input == nil {
		return Params{}, nil
	}

	switch v := input.(type) {
	case Params:
		// Already Params, return as-is
		return v, nil

	case map[string]string:
		return NewSimpleParams(v), nil

	case map[string]any:
		// Check if all values are strings
		allStrings := true
		for _, val := range v {
			if _, ok := val.(string); !ok {
				allStrings = false
				break
			}
		}

		if allStrings {
			// Convert to Simple for optimization
			simple := make(map[string]string, len(v))
			for k, val := range v {
				simple[k] = val.(string)
			}
			return NewSimpleParams(simple), nil
		}

		// Keep as Rich
		return NewRichParams(v), nil

	case json.RawMessage:
		return NewRawParams(v), nil

	case []byte:
		return NewRawParams(json.RawMessage(v)), nil

	case string:
		// Try to parse as JSON
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return Params{}, fmt.Errorf("failed to parse string as JSON: %w", err)
		}
		return ParseParams(m)

	default:
		// Try to marshal and then unmarshal
		data, err := json.Marshal(v)
		if err != nil {
			return Params{}, fmt.Errorf("unsupported param type %T: %w", input, err)
		}
		return NewRawParams(data), nil
	}
}

// Type returns which param type is active
func (p *Params) Type() ParamType {
	if p.Simple != nil {
		return ParamTypeString
	}
	if p.Rich != nil {
		return ParamTypeAny
	}
	if p.Raw != nil {
		return ParamTypeRaw
	}
	return ParamTypeUnknown
}

// IsEmpty returns true if no params are set
func (p *Params) IsEmpty() bool {
	return p.Type() == ParamTypeUnknown
}

// AsStringMap returns all parameters as map[string]string
// Non-string values are converted using fmt.Sprintf
func (p *Params) AsStringMap() (map[string]string, error) {
	switch p.Type() {
	case ParamTypeString:
		return p.Simple, nil
	case ParamTypeAny:
		result := make(map[string]string, len(p.Rich))
		for k, v := range p.Rich {
			result[k] = fmt.Sprintf("%v", v)
		}
		return result, nil
	case ParamTypeRaw:
		m, err := p.parseRaw()
		if err != nil {
			return nil, err
		}
		result := make(map[string]string, len(m))
		for k, v := range m {
			result[k] = fmt.Sprintf("%v", v)
		}
		return result, nil
	case ParamTypeUnknown:
		fallthrough
	default:
		return make(map[string]string), nil
	}
}

// parseRaw parses Raw field on demand
func (p *Params) parseRaw() (map[string]any, error) {
	if p.Raw == nil {
		return nil, fmt.Errorf("raw params is nil")
	}

	var result map[string]any
	if err := json.Unmarshal(p.Raw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse raw params: %w", err)
	}

	return result, nil
}

// MarshalJSON implements json.Marshaler
func (p *Params) MarshalJSON() ([]byte, error) {
	// Serialize whichever field is set
	switch p.Type() {
	case ParamTypeString:
		return json.Marshal(p.Simple)
	case ParamTypeAny:
		return json.Marshal(p.Rich)
	case ParamTypeRaw:
		return p.Raw, nil
	case ParamTypeUnknown:
		fallthrough
	default:
		return []byte("{}"), nil
	}
}

// UnmarshalJSON implements json.Unmarshaler
func (p *Params) UnmarshalJSON(data []byte) error {
	// Handle null
	if string(data) == "null" {
		return nil
	}

	// Try to unmarshal as map
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	// Check if all values are strings (backward compat)
	allStrings := true
	for _, v := range m {
		if _, ok := v.(string); !ok {
			allStrings = false
			break
		}
	}

	if allStrings {
		// Convert to Simple
		p.Simple = make(map[string]string, len(m))
		for k, v := range m {
			p.Simple[k] = v.(string)
		}
	} else {
		// Store as Rich
		p.Rich = m
	}

	return nil
}
