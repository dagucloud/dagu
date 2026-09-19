// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Connections are pooled across attempts; credentials remain request-scoped.
var sharedHTTPClient = llm.NewHTTPClient(llm.DefaultConfig())

type client struct {
	maxResponseBytes int64
	http             *llm.HTTPClient
	endpoint         string
	apiKey           string
}

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]question `json:"questions"`
}

func (c *client) evaluate(ctx context.Context, req request) (map[string]any, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode decision request: %w", err)
	}
	response, err := c.http.Do(ctx, c.endpoint, body, map[string]string{"Authorization": "Bearer " + c.apiKey})
	if err != nil {
		if apiErr, ok := errors.AsType[*llm.APIError](err); ok {
			return nil, fmt.Errorf("decision request failed (HTTP %d)", apiErr.StatusCode)
		}
		return nil, fmt.Errorf("decision request failed: %w", err)
	}
	defer func() { _ = response.Close() }()

	data, err := io.ReadAll(io.LimitReader(response, c.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read decision response: %w", err)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return nil, fmt.Errorf("decision response exceeded maximum size limit of %d bytes", c.maxResponseBytes)
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decision response must be a JSON object")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("decision response must contain one JSON object")
	}
	if err := validateResponse(result, req.Questions); err != nil {
		return nil, err
	}
	return result, nil
}

func validateResponse(result map[string]any, questions map[string]question) error {
	model, ok := result["model"].(string)
	if !ok || model == "" {
		return fmt.Errorf("decision response: missing model")
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok {
		return fmt.Errorf("decision response: missing usage")
	}
	for _, field := range []string{"input_tokens", "output_tokens"} {
		v, ok := number(usage[field])
		if !ok || v < 0 || v != float64(int64(v)) {
			return fmt.Errorf("decision response: invalid usage.%s", field)
		}
	}
	answers, ok := result["answers"].(map[string]any)
	if !ok {
		return fmt.Errorf("decision response: missing answers")
	}
	for id, q := range questions {
		answer, ok := answers[id].(map[string]any)
		if !ok || answer["type"] != q.Type {
			return fmt.Errorf("decision response: missing or mismatched answer for %q", id)
		}
		if err := validateAnswer(answer, q); err != nil {
			return fmt.Errorf("decision response: answer %q: %w", id, err)
		}
	}
	return nil
}

func validateAnswer(answer map[string]any, q question) error {
	if q.Type == noulType {
		if !probability(answer["noul"]) {
			return fmt.Errorf("noul must be a probability")
		}
		return nil
	}
	if !probability(answer["confidence"]) {
		return fmt.Errorf("confidence must be a probability")
	}
	probabilities, ok := answer["probabilities"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing probabilities")
	}
	var options []string
	if q.Type == choiceType {
		criteria := q.Criteria.(map[string]any)
		choice, ok := answer["choice"].(string)
		if _, exists := criteria[choice]; !ok || !exists {
			return fmt.Errorf("choice must be a configured option")
		}
		for option := range criteria {
			options = append(options, option)
		}
	} else {
		criteria := q.Criteria.([]any)
		score, ok := number(answer["score"])
		if !ok || score < 0 || score > float64(len(criteria)-1) {
			return fmt.Errorf("score must be within the configured scale")
		}
		legend, ok := answer["legend"].(map[string]any)
		if !ok || len(legend) != len(criteria) {
			return fmt.Errorf("legend must describe each configured level")
		}
		for i := range criteria {
			option := strconv.Itoa(i)
			if _, ok := legend[option].(string); !ok {
				return fmt.Errorf("legend must describe each configured level")
			}
			options = append(options, option)
		}
	}
	if len(probabilities) != len(options) {
		return fmt.Errorf("probabilities must cover the configured options")
	}
	for _, option := range options {
		if !probability(probabilities[option]) {
			return fmt.Errorf("probabilities must cover the configured options")
		}
	}
	return nil
}

func probability(v any) bool {
	n, ok := number(v)
	return ok && n >= 0 && n <= 1
}

func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	value, err := n.Float64()
	return value, err == nil && !math.IsInf(value, 0) && !math.IsNaN(value)
}
