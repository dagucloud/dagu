// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package aigateway is the model gateway of Spec 094. It has the rules that the
// relay of the worker and the coordinator share: the allowed requests, the
// failure kinds and the limits. It also has the code of the coordinator that
// calls the provider, which the local mode runs in the process of the run.
//
// No type of this package holds a key in a form that a log or an error can
// show. The key exists in memory only while a request goes to the provider.
package aigateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// Reason is a name of the table of failure kinds of Spec 094, "Failure kinds".
type Reason string

// The reasons of the gateway. The reasons that concern an endpoint are the
// reasons of the package aiendpoint.
const (
	ReasonRelayUnauthorized Reason = "relay_unauthorized"
	ReasonHostNotAllowed    Reason = "host_not_allowed"
	ReasonPathNotAllowed    Reason = "path_not_allowed"
	ReasonMethodNotAllowed  Reason = "method_not_allowed"
	ReasonRequestTooLarge   Reason = "request_too_large"
	ReasonResponseTooLarge  Reason = "response_too_large"
	ReasonTooManyStreams    Reason = "too_many_streams"
	ReasonRelayUnavailable  Reason = "relay_unavailable"
	ReasonUpstreamError     Reason = "upstream_error"
	ReasonUpstreamTimeout   Reason = "upstream_timeout"
	// ReasonCanceled is not a failure kind. It marks a request that the client,
	// the end of the attempt or a lost lease ended.
	ReasonCanceled Reason = "canceled"

	ReasonEndpointUnknown          = Reason(aiendpoint.ReasonUnknown)
	ReasonEndpointForbidden        = Reason(aiendpoint.ReasonForbidden)
	ReasonEndpointProtocolMismatch = Reason(aiendpoint.ReasonProtocolMismatch)
	ReasonEndpointKeyMissing       = Reason(aiendpoint.ReasonKeyMissing)
	ReasonEndpointUnavailable      = Reason(aiendpoint.ReasonUnavailable)
	ReasonModelUnknown             = Reason(aiendpoint.ReasonModelUnknown)
	ReasonMCPServerUnknown         = Reason(aiendpoint.ReasonMCPUnknown)
	ReasonMCPServerForbidden       = Reason(aiendpoint.ReasonMCPForbidden)

	// ReasonMCPToolNotAllowed is the reason of a tools/call for a tool that is not in
	// the key allow. The answer is a JSON-RPC error with the status 200.
	ReasonMCPToolNotAllowed Reason = "mcp_tool_not_allowed"
	// ReasonMCPRequestInvalid is the reason of a MCP request whose body is not valid
	// JSON-RPC.
	ReasonMCPRequestInvalid Reason = "mcp_request_invalid"
)

// statusOf is the HTTP status of each reason, as in Spec 094.
var statusOf = map[Reason]int{
	ReasonRelayUnauthorized:        http.StatusUnauthorized,
	ReasonHostNotAllowed:           http.StatusForbidden,
	ReasonPathNotAllowed:           http.StatusNotFound,
	ReasonMethodNotAllowed:         http.StatusMethodNotAllowed,
	ReasonEndpointUnknown:          http.StatusNotFound,
	ReasonEndpointForbidden:        http.StatusForbidden,
	ReasonEndpointProtocolMismatch: http.StatusBadRequest,
	ReasonEndpointKeyMissing:       http.StatusServiceUnavailable,
	ReasonModelUnknown:             http.StatusNotFound,
	ReasonMCPServerUnknown:         http.StatusNotFound,
	ReasonMCPServerForbidden:       http.StatusForbidden,
	ReasonMCPToolNotAllowed:        http.StatusOK,
	ReasonMCPRequestInvalid:        http.StatusBadRequest,
	ReasonRequestTooLarge:          http.StatusRequestEntityTooLarge,
	ReasonResponseTooLarge:         http.StatusBadGateway,
	ReasonTooManyStreams:           http.StatusServiceUnavailable,
	ReasonEndpointUnavailable:      http.StatusServiceUnavailable,
	ReasonRelayUnavailable:         http.StatusServiceUnavailable,
	ReasonUpstreamError:            http.StatusBadGateway,
	ReasonUpstreamTimeout:          http.StatusGatewayTimeout,
	// 499 is the status that proxies use for a client that went away.
	ReasonCanceled: 499,
}

// Known reports whether the reason is in the table.
func (r Reason) Known() bool {
	_, ok := statusOf[r]
	return ok
}

// Status returns the HTTP status that the client gets for the reason. An unknown
// reason gives 502.
func (r Reason) Status() int {
	if status, ok := statusOf[r]; ok {
		return status
	}
	return http.StatusBadGateway
}

// Retryable reports whether a later request can get another answer for the reason.
// A refusal that does not depend on time is not retryable: the relay tells the client
// so with the header x-should-retry.
func (r Reason) Retryable() bool {
	switch r { //nolint:exhaustive // each other reason does not change with time
	case ReasonTooManyStreams, ReasonRelayUnavailable, ReasonEndpointUnavailable,
		ReasonUpstreamError, ReasonUpstreamTimeout:
		return true
	}
	return false
}

// Failure is a refusal or a failure of the gateway. It has a reason, and a
// message that never holds a key or a header value.
type Failure struct {
	Reason  Reason
	Message string
}

// Fail returns a Failure with the formatted message.
func Fail(reason Reason, format string, args ...any) *Failure {
	return &Failure{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

func (f *Failure) Error() string {
	if f.Message == "" {
		return string(f.Reason)
	}
	return string(f.Reason) + ": " + f.Message
}

// Is matches a failure with the same reason.
func (f *Failure) Is(target error) bool {
	other, ok := target.(*Failure)
	return ok && other.Reason == f.Reason
}

// FailureOf returns the failure that err holds, or a failure with the reason
// relay_unavailable that wraps err.
func FailureOf(err error) *Failure {
	if err == nil {
		return nil
	}
	if failure, ok := errors.AsType[*Failure](err); ok {
		return failure
	}
	if endpointErr, ok := errors.AsType[*aiendpoint.Error](err); ok {
		return &Failure{Reason: Reason(endpointErr.Reason), Message: endpointErr.Detail}
	}
	return &Failure{Reason: ReasonRelayUnavailable, Message: err.Error()}
}

// ParseFailure reads the text "<reason>: <message>" that the coordinator puts in
// the message of a gRPC error. It returns false when the text starts with no
// known reason.
func ParseFailure(text string) (*Failure, bool) {
	name, message, _ := strings.Cut(text, ": ")
	if reason := Reason(name); reason.Known() {
		return &Failure{Reason: reason, Message: message}, true
	}
	return nil, false
}

// ErrorBody returns the content type and the body that a client of the protocol
// gets for a failure, in the error format of its API. The message holds the name
// of the reason.
func ErrorBody(protocol aiendpoint.Protocol, f *Failure) (string, []byte) {
	message := f.Error()
	var body any
	switch protocol { //nolint:exhaustive // the protocol anthropic is the default
	case aiendpoint.ProtocolOpenAI:
		body = map[string]any{"error": map[string]any{
			"message": message, "type": "dagu_relay_error", "code": string(f.Reason),
		}}
	default:
		body = map[string]any{"type": "error", "error": map[string]any{
			"type": "dagu_relay_error", "message": message,
		}}
	}
	data, err := json.Marshal(body)
	if err != nil {
		data = []byte(`{"error":"` + string(f.Reason) + `"}`)
	}
	return "application/json", data
}
