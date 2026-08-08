// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const maxJSONNestingDepth = 100

// Input contains decoded completion values and the requested coercion policy.
type Input struct {
	Values        map[string]any
	CoerceStrings bool
}

// ParseJSONInput decodes exactly one non-null JSON object without losing number precision.
func ParseJSONInput(raw []byte) (Input, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoded, err := decodeUniqueJSONValue(decoder, 0)
	if err != nil {
		return Input{}, errorf(ErrorInvalid, "invalid JSON value: %v", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Input{}, errorf(ErrorInvalid, "invalid JSON value: %v", err)
	}
	values, ok := decoded.(map[string]any)
	if !ok || values == nil {
		return Input{}, errorf(ErrorInvalid, "input must be a JSON object")
	}
	return Input{Values: values}, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder, depth int) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	if depth >= maxJSONNestingDepth {
		return nil, fmt.Errorf("JSON nesting depth exceeds %d", maxJSONNestingDepth)
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON object member name must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON member %q", key)
			}
			value, err := decodeUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func prepareCompletion(dag *ir.DAG, node *exec.Node, input Input) (*spec.HumanTaskInputResult, string, error) {
	result, err := spec.ValidateHumanTaskInputs(node.Step.HumanTask.Form, input.Values, input.CoerceStrings)
	if err != nil {
		return nil, "", errorf(ErrorInvalid, "invalid input for human task step %q: %v", node.Step.ID, err)
	}
	outputs, err := marshalOutputs(dag, result)
	if err != nil {
		return nil, "", errorf(ErrorInvalid, "human task step %q: %v", node.Step.ID, err)
	}
	return result, outputs, nil
}

func marshalOutputs(dag *ir.DAG, result *spec.HumanTaskInputResult) (string, error) {
	maxSize := dag.MaxOutputSize
	if maxSize == 0 {
		maxSize = ir.DefaultMaxOutputSize
	}
	if len(result.Canonical) > maxSize {
		return "", fmt.Errorf("human task input exceeded maximum size limit of %d bytes", maxSize)
	}
	if len(result.Outputs) == 0 {
		return "", nil
	}
	outputsData, err := json.Marshal(result.Outputs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal human task outputs: %w", err)
	}
	if len(outputsData) > maxSize {
		return "", fmt.Errorf("human task step outputs exceeded maximum size limit of %d bytes", maxSize)
	}
	return string(outputsData), nil
}
