// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = "a2a"
	opSend       = "send"
	opGetTask    = "get_task"

	maxIDLength = 512
)

// reservedHeaders are set by the client or the HTTP stack.
var reservedHeaders = map[string]struct{}{
	"content-type": {}, "accept": {}, "a2a-version": {}, "host": {}, "content-length": {},
	// Go replays a POST that carries one of these after a dropped connection.
	"idempotency-key": {}, "x-idempotency-key": {},
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

type common struct {
	AgentURL string            `mapstructure:"agent_url"`
	Headers  map[string]string `mapstructure:"headers"`
	Tenant   string            `mapstructure:"tenant"`
}

type sendConfig struct {
	common            `mapstructure:",squash"`
	Message           string  `mapstructure:"message"`
	ContextID         string  `mapstructure:"context_id"`
	MessageID         *string `mapstructure:"message_id"`
	ReturnImmediately *bool   `mapstructure:"return_immediately"`
}

type getTaskConfig struct {
	common        `mapstructure:",squash"`
	TaskID        string `mapstructure:"task_id"`
	HistoryLength *int   `mapstructure:"history_length"`
}

// config is the decoded with: block of either action.
type config struct {
	op   string
	send sendConfig
	get  getTaskConfig
}

func (c config) base() common {
	if c.op == opSend {
		return c.send.common
	}
	return c.get.common
}

func stepOperation(step ir.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

// intHook lets a whole-value reference resolve to an integer string at runtime.
func intHook(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.Int {
		return data, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(data.(string)))
	if err != nil {
		return nil, errors.New("must be an integer")
	}
	return n, nil
}

func decode(raw map[string]any, result any) error {
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{ErrorUnused: true, DecodeHook: intHook, Result: result})
	if err != nil {
		return err
	}
	return dec.Decode(raw)
}

// decodeConfig decodes with.*. At load time a field that holds an unresolved
// reference is accepted and checked again once resolved at runtime.
func decodeConfig(step ir.Step, loadTime bool) (config, error) {
	cfg := config{op: stepOperation(step)}
	raw := step.ExecutorConfig.Config
	if raw == nil {
		raw = map[string]any{}
	}
	if s, ok := raw["history_length"].(string); ok && loadTime && cfg.op == opGetTask && value.HasAnyReference(s) {
		raw = maps.Clone(raw)
		delete(raw, "history_length")
	}
	var err error
	switch cfg.op {
	case opSend:
		err = decode(raw, &cfg.send)
	case opGetTask:
		err = decode(raw, &cfg.get)
	default:
		return cfg, fmt.Errorf("a2a: unknown action %q", cfg.op)
	}
	if err != nil {
		return cfg, fmt.Errorf("a2a.%s: invalid with: %w", cfg.op, err)
	}
	return cfg, cfg.validate(loadTime)
}

func (c config) validate(loadTime bool) error {
	prefix := "a2a." + c.op + ": "
	// A field holding a reference is validated once it is resolved.
	skip := func(s string) bool { return loadTime && value.HasAnyReference(s) }
	b := c.base()
	if err := validateAgentURL(b.AgentURL, skip(b.AgentURL)); err != nil {
		return errors.New(prefix + err.Error())
	}
	for name, v := range b.Headers {
		if !headerName.MatchString(name) {
			return fmt.Errorf("%swith.headers has an invalid header name %q", prefix, name)
		}
		if _, reserved := reservedHeaders[strings.ToLower(name)]; reserved {
			return fmt.Errorf("%swith.headers.%s is reserved (set by the client, or able to make the HTTP stack replay the request) and cannot be overridden", prefix, name)
		}
		if !skip(v) && strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("%swith.headers.%s must not contain line breaks", prefix, name)
		}
	}
	fields := map[string]string{"with.tenant": b.Tenant}
	switch c.op {
	case opSend:
		if strings.TrimSpace(c.send.Message) == "" {
			return errors.New(prefix + "with.message is required")
		}
		fields["with.context_id"] = c.send.ContextID
		if id := c.send.MessageID; id != nil {
			// An explicit ID is never replaced by the default, so an empty
			// value is an error.
			if *id == "" {
				return errors.New(prefix + "with.message_id must not be empty")
			}
			fields["with.message_id"] = *id
		}
	case opGetTask:
		if strings.TrimSpace(c.get.TaskID) == "" {
			return errors.New(prefix + "with.task_id is required")
		}
		fields["with.task_id"] = c.get.TaskID
		if h := c.get.HistoryLength; h != nil && *h < 0 {
			return errors.New(prefix + "with.history_length must not be negative")
		}
	}
	for name, v := range fields {
		if skip(v) {
			continue
		}
		if len(v) > maxIDLength || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return fmt.Errorf("%s%s must be at most %d characters without control characters", prefix, name, maxIDLength)
		}
	}
	return nil
}

func validateAgentURL(raw string, deferred bool) error {
	if raw == "" {
		return errors.New("with.agent_url is required")
	}
	if deferred {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return errors.New("with.agent_url must be an absolute https URL")
	}
	if u.User != nil {
		return errors.New("with.agent_url must not contain credentials; use with.headers")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return errors.New("with.agent_url must not contain a query string or fragment")
	}
	return nil
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	_, err := decodeConfig(step, true)
	return err
}

func str(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc}
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Required:             []string{"agent_url"},
	Properties: map[string]*jsonschema.Schema{
		"agent_url":          str("HTTPS URL of the agent; its Agent Card is read from /.well-known/agent-card.json below it. Must not contain credentials, a query string, or a fragment."),
		"headers":            {Type: "object", Description: "HTTP headers for Agent Card and JSON-RPC requests to the agent_url origin, for example Authorization: Bearer ${SECRET}.", AdditionalProperties: &jsonschema.Schema{Type: "string"}},
		"tenant":             str("Tenant to route to; defaults to the tenant of the selected Agent Card interface."),
		"message":            str("a2a.send: text of the user message."),
		"context_id":         str("a2a.send: context to continue."),
		"message_id":         str("a2a.send: messageId to send, as given. Defaults to a UUIDv5 of the run attempt ID, step name, repeat index, and message, so a step retry resends the same ID."),
		"return_immediately": {Type: "boolean", Description: "a2a.send: request nonblocking execution (default true)."},
		"task_id":            str("a2a.get_task: ID of the task to read."),
		"history_length":     {AnyOf: []*jsonschema.Schema{{Type: "integer", Minimum: new(0.0)}, {Type: "string"}}, Description: "a2a.get_task: number of most recent history messages."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
