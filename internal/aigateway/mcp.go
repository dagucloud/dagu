// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// Spec 095, "The key allow". The coordinator parses each POST body of the MCP
// relay. A tools/call for a tool that is not in the key allow gets a JSON-RPC error
// and no call to the server.

const (
	// jsonRPCInvalidRequest is the JSON-RPC code of a body that is not valid.
	jsonRPCInvalidRequest = -32600
	// jsonRPCServerError is the code of a refusal of the relay. The range -32000 to
	// -32099 is for the errors of a server.
	jsonRPCServerError = -32000
	// jsonRPCToolNotAllowed is the code of a tool that is not in allow.
	jsonRPCToolNotAllowed = -32001
)

// rpcRequestItem is a JSON-RPC request of a body: it has an id and expects an answer.
type rpcRequestItem struct {
	id json.RawMessage
}

// mcpInspection is what the coordinator learns from the body of a POST request.
type mcpInspection struct {
	// methods and tools list the JSON-RPC methods of the body and the tool names of
	// its tools/call items, for the audit line.
	methods []string
	tools   []string
	// requests are the items that have an id, in the order of the body.
	requests []rpcRequestItem
	// batch is true for a JSON array.
	batch bool
	// refused is the first tools/call that names a tool outside allow, or nil.
	refused *mcpRefusal
}

// mcpRefusal names the tool of a refused tools/call.
type mcpRefusal struct {
	tool string
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// maxMCPJSONDepth is the deepest nesting of arrays and objects that the relay accepts.
const maxMCPJSONDepth = 64

// The keys that the inspector reads, for each place where it reads them. Go matches
// a JSON key with no regard to case, and it keeps the last of two equal keys. A MCP
// server usually does neither. The relay refuses a body where the two readings can
// differ, so the inspector and the server read the same message.
var (
	mcpMessageKeys = []string{"jsonrpc", "id", "method", "params", "result", "error"}
	mcpParamsKeys  = []string{"name", "arguments"}
)

// mcpPlace is the place of an object in the body.
type mcpPlace int

const (
	// placeOther is an object or an array that the inspector does not read.
	placeOther mcpPlace = iota
	// placeMessage is a JSON-RPC message: the body, or an item of a batch.
	placeMessage
	// placeParams is the value of the key params of a message.
	placeParams
)

// checkMCPJSON refuses a body that two JSON parsers can read in two ways. It refuses
// a duplicate key at any depth, a key that differs only by case from a key that the
// inspector reads, a body that is not UTF-8, a body that is too deep and a body with
// data after the first value. It reads the tokens of the body and keeps no copy: the
// relay sends the bytes that the inspector read.
func checkMCPJSON(body []byte) error {
	if !utf8.Valid(body) {
		return errors.New("the body is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := walkMCPJSON(dec, 0, placeMessage); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the body has data after the first JSON value")
	}
	return nil
}

func walkMCPJSON(dec *json.Decoder, depth int, place mcpPlace) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("the body is not valid JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= maxMCPJSONDepth {
		return fmt.Errorf("the body has more than %d levels", maxMCPJSONDepth)
	}
	switch delim {
	case '[':
		// A top array is a batch: its items are messages.
		itemPlace := placeOther
		if depth == 0 {
			itemPlace = placeMessage
		}
		for dec.More() {
			if err := walkMCPJSON(dec, depth+1, itemPlace); err != nil {
				return err
			}
		}
	case '{':
		var protected []string
		switch place {
		case placeMessage:
			protected = mcpMessageKeys
		case placeParams:
			protected = mcpParamsKeys
		case placeOther:
		}
		seen := map[string]struct{}{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("the body is not valid JSON: %w", err)
			}
			key, _ := keyTok.(string)
			if _, dup := seen[key]; dup {
				return fmt.Errorf("the key %q occurs two times in an object", key)
			}
			seen[key] = struct{}{}
			for _, name := range protected {
				if key != name && strings.EqualFold(key, name) {
					return fmt.Errorf("the key %q differs only by case from the key %q", key, name)
				}
			}
			childPlace := placeOther
			if place == placeMessage && key == "params" {
				childPlace = placeParams
			}
			if err := walkMCPJSON(dec, depth+1, childPlace); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("the body is not valid JSON: %w", err)
	}
	return nil
}

// checkMCPMethod refuses a method that is not a plain ASCII name. Every method of
// MCP is one. A name with a space, a control or a Unicode character, or with the
// form of tools/call in other case, can be read as tools/call by a server that is
// not strict, and the allow list would not apply.
func checkMCPMethod(method string) error {
	for i := 0; i < len(method); i++ {
		c := method[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '/', c == '_', c == '-', c == '.':
		default:
			return fmt.Errorf("the method %s is not a plain ASCII name", quote(method))
		}
	}
	if method != "tools/call" && strings.EqualFold(method, "tools/call") {
		return fmt.Errorf("the method %s is not tools/call in lower case", quote(method))
	}
	return nil
}

// inspectMCPBody parses the body of a POST request as JSON-RPC: one message, or a
// batch that it parses item by item. It returns the reason mcp_request_invalid for
// a body that is not valid JSON-RPC. The allow list is the key allow of the entry.
func inspectMCPBody(body []byte, allow []string) (*mcpInspection, *Failure) {
	trimmed := bytes.TrimSpace(body)
	invalid := func(format string, args ...any) (*mcpInspection, *Failure) {
		return nil, Fail(ReasonMCPRequestInvalid, format, args...)
	}
	if len(trimmed) == 0 {
		return invalid("the body is empty")
	}
	if err := checkMCPJSON(trimmed); err != nil {
		return invalid("%v", err)
	}
	var items []json.RawMessage
	insp := &mcpInspection{}
	if trimmed[0] == '[' {
		insp.batch = true
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return invalid("the body is not a JSON-RPC batch")
		}
		if len(items) == 0 {
			return invalid("the batch is empty")
		}
	} else {
		items = []json.RawMessage{trimmed}
	}
	for _, raw := range items {
		var msg rpcMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return invalid("the body is not a JSON-RPC message")
		}
		if msg.JSONRPC != "2.0" {
			return invalid("the field jsonrpc must be 2.0")
		}
		isCall := msg.Method != ""
		isReply := len(msg.Result) > 0 || len(msg.Error) > 0
		if isCall == isReply {
			return invalid("a message has a method, or a result or an error")
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
		if isCall {
			if err := checkMCPMethod(msg.Method); err != nil {
				return invalid("%v", err)
			}
			insp.methods = append(insp.methods, msg.Method)
			if hasID {
				insp.requests = append(insp.requests, rpcRequestItem{id: msg.ID})
			}
		}
		if msg.Method != "tools/call" {
			continue
		}
		var params struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.Name == "" {
			return invalid("a tools/call has no tool name")
		}
		insp.tools = append(insp.tools, params.Name)
		if !slices.Contains(allow, params.Name) && insp.refused == nil {
			insp.refused = &mcpRefusal{tool: params.Name}
		}
	}
	return insp, nil
}

// refusalBody returns the answer to a body that holds a refused tools/call: the
// JSON-RPC error of the item that has an id. A batch gets an array with one error
// for each request, because Dagu refuses the whole batch. A body with no request
// has no answer: the caller sends the status 202 with no body.
func (i *mcpInspection) refusalBody(server string) []byte {
	message := string(ReasonMCPToolNotAllowed) + ": the tool " + quote(i.refused.tool) +
		" is not in allow of the MCP server " + quote(server)
	if i.batch {
		message = string(ReasonMCPToolNotAllowed) + ": the batch has a call of the tool " + quote(i.refused.tool) +
			", which is not in allow of the MCP server " + quote(server) + ", so no item ran"
	}
	if len(i.requests) == 0 {
		return nil
	}
	answers := make([]map[string]any, 0, len(i.requests))
	for _, request := range i.requests {
		answers = append(answers, rpcError(request.id, jsonRPCToolNotAllowed, message))
	}
	var out any = answers[0]
	if i.batch {
		out = answers
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return data
}

func rpcError(id json.RawMessage, code int, message string) map[string]any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
}

func quote(text string) string {
	data, err := json.Marshal(text)
	if err != nil {
		return `""`
	}
	return string(data)
}

// MCPErrorBody returns the content type and the body that a MCP client gets for a
// failure of the relay: a JSON-RPC error. The message holds the name of the reason.
func MCPErrorBody(f *Failure) (string, []byte) {
	code := jsonRPCServerError
	if f.Reason == ReasonMCPRequestInvalid {
		code = jsonRPCInvalidRequest
	}
	data, err := json.Marshal(rpcError(nil, code, f.Error()))
	if err != nil {
		data = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"` + string(f.Reason) + `"}}`)
	}
	return "application/json", data
}

// joinAudit joins the names of an audit field. The audit line has the methods and
// the tool names of a body.
func joinAudit(names []string) string {
	return strings.Join(names, ",")
}
