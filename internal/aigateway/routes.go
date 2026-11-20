// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// Spec 094, "Allowed requests". The relay and the coordinator refuse each request
// that is not in the list of the protocol.

const (
	// MaxQueryBytes is the longest query of a request.
	MaxQueryBytes = 2048
	// maxModelIDChars is the longest {id} of a path.
	maxModelIDChars = 200
)

// Route names, as the audit line writes them.
const (
	RouteMessages        = "messages"
	RouteCountTokens     = "count_tokens"
	RouteChatCompletions = "chat_completions"
	RouteResponses       = "responses"
	RouteModels          = "models"
)

// Route is an allowed request.
type Route struct {
	// Name is the name of the route, for the audit line.
	Name string
	// Path is the path of the request, after the check.
	Path string
	// Query is the query after the check, with the allowed keys only.
	Query string
}

type routeRule struct {
	method string
	name   string
	// queryKeys are the keys that the query can hold.
	queryKeys []string
}

// anthropicRoutes and openaiRoutes list the exact paths. The path of a model
// has its own check.
var (
	anthropicRoutes = map[string]routeRule{
		"/v1/messages":              {http.MethodPost, RouteMessages, []string{"beta"}},
		"/v1/messages/count_tokens": {http.MethodPost, RouteCountTokens, []string{"beta"}},
		"/v1/models":                {http.MethodGet, RouteModels, []string{"limit", "after_id", "before_id"}},
	}
	openaiRoutes = map[string]routeRule{
		"/v1/chat/completions": {http.MethodPost, RouteChatCompletions, nil},
		"/v1/responses":        {http.MethodPost, RouteResponses, nil},
		"/v1/models":           {http.MethodGet, RouteModels, nil},
	}
)

// MCPPathPrefix starts the path of a MCP server entry on the relay.
const MCPPathPrefix = "/mcp/"

// RouteMCP is the name of the route of the MCP relay, for the audit line.
const RouteMCP = "mcp"

// IsMCPPath reports whether the path belongs to the MCP relay.
func IsMCPPath(path string) bool {
	return path == "/mcp" || strings.HasPrefix(path, MCPPathPrefix)
}

// AllowMCP checks a request for the MCP relay against the list of Spec 095, "The
// MCP relay": POST, GET and DELETE on /mcp/<name>, with no query and no other path.
// It returns the name of the server entry, or a failure with the reason
// path_not_allowed or method_not_allowed.
func AllowMCP(method, path, rawQuery string) (string, *Failure) {
	name, ok := strings.CutPrefix(path, MCPPathPrefix)
	if !ok || strings.Contains(name, "/") || aiendpoint.ValidateMCPName(name) != nil {
		return "", Fail(ReasonPathNotAllowed, "the path is not /mcp/<name>")
	}
	switch method {
	case http.MethodPost, http.MethodGet, http.MethodDelete:
	default:
		return "", Fail(ReasonMethodNotAllowed, "the method %s is not allowed for the MCP relay", method)
	}
	if rawQuery != "" {
		return "", Fail(ReasonPathNotAllowed, "the MCP relay allows no query")
	}
	return name, nil
}

// modelsPrefix starts the path of one model.
const modelsPrefix = "/v1/models/"

// Allow checks a request of a client against the list of the protocol. It
// returns the route, or a failure with the reason path_not_allowed (the path or
// the query is not in the list) or method_not_allowed (the method is not in the
// list of the path). The path is the path that the client sent to the relay.
func Allow(protocol aiendpoint.Protocol, method, path, rawQuery string) (Route, *Failure) {
	routes := anthropicRoutes
	if protocol == aiendpoint.ProtocolOpenAI {
		routes = openaiRoutes
	}
	rule, ok := routes[path]
	if !ok && strings.HasPrefix(path, modelsPrefix) {
		if !validModelID(strings.TrimPrefix(path, modelsPrefix)) {
			return Route{}, Fail(ReasonPathNotAllowed, "the path of the model is not valid")
		}
		rule, ok = routes["/v1/models"], true
	}
	if !ok {
		return Route{}, Fail(ReasonPathNotAllowed, "the path is not in the list of the protocol %s", protocol)
	}
	if method != rule.method {
		return Route{}, Fail(ReasonMethodNotAllowed, "the method %s is not allowed for this path", method)
	}
	query, failure := checkQuery(rawQuery, rule.queryKeys)
	if failure != nil {
		return Route{}, failure
	}
	return Route{Name: rule.name, Path: path, Query: query}, nil
}

// validModelID checks the {id} of the path of a model: at most 200 characters
// from A-Za-z0-9._:/- and no "..".
func validModelID(id string) bool {
	if id == "" || len(id) > maxModelIDChars || strings.Contains(id, "..") {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}

// checkQuery returns the query when each key is in the list, and the query has at
// most 2048 bytes. The result is the encoded form of the values that the client
// sent.
func checkQuery(raw string, allowed []string) (string, *Failure) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > MaxQueryBytes {
		return "", Fail(ReasonPathNotAllowed, "the query has more than %d bytes", MaxQueryBytes)
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", Fail(ReasonPathNotAllowed, "the query is not valid")
	}
	for key := range values {
		if !slices.Contains(allowed, key) {
			return "", Fail(ReasonPathNotAllowed, "the query key %q is not allowed", key)
		}
	}
	return values.Encode(), nil
}

// UpstreamPath returns the path of the call to the provider. For the protocol
// anthropic, the path of the request goes to the base URL as it is. For the
// protocol openai, the first segment /v1 is removed. basePath is the path of the
// base URL.
func UpstreamPath(protocol aiendpoint.Protocol, basePath, requestPath string) string {
	rest := requestPath
	if protocol == aiendpoint.ProtocolOpenAI {
		rest = strings.TrimPrefix(requestPath, "/v1")
	}
	return strings.TrimSuffix(basePath, "/") + rest
}

// allowedRequestHeaders are the request headers that the coordinator sends to
// the provider. The names are in canonical form.
var allowedRequestHeaders = []string{
	"Content-Type", "Accept", "User-Agent", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta",
}

// allowedMCPRequestHeaders are the request headers that the coordinator sends to a
// MCP server (Spec 095, "The MCP relay"). The names are in canonical form.
var allowedMCPRequestHeaders = []string{
	"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id", "User-Agent",
}

// AllowedRequestHeaders returns the request headers that reach the provider.
func AllowedRequestHeaders() []string { return slices.Clone(allowedRequestHeaders) }

// AllowedMCPRequestHeaders returns the request headers that reach a MCP server.
func AllowedMCPRequestHeaders() []string { return slices.Clone(allowedMCPRequestHeaders) }

// FilterRequestHeaders returns the headers of the allowed list. A request with
// content-encoding gives a failure with the reason host_not_allowed: the form of
// the request is not allowed. It returns the size of the kept headers, names and
// values, for the limit.
func FilterRequestHeaders(in http.Header) (http.Header, int, *Failure) {
	return filterHeaders(in, allowedRequestHeaders)
}

// FilterMCPRequestHeaders is FilterRequestHeaders for a request to a MCP server.
func FilterMCPRequestHeaders(in http.Header) (http.Header, int, *Failure) {
	return filterHeaders(in, allowedMCPRequestHeaders)
}

func filterHeaders(in http.Header, allowed []string) (http.Header, int, *Failure) {
	if in.Get("Content-Encoding") != "" {
		return nil, 0, Fail(ReasonHostNotAllowed, "a request with content-encoding is not allowed")
	}
	out := http.Header{}
	size := 0
	for _, name := range allowed {
		for _, value := range in.Values(name) {
			out.Add(name, value)
			size += len(name) + len(value)
		}
	}
	return out, size, nil
}

// hopByHop are the response headers that belong to one connection.
var hopByHop = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer",
	"Transfer-Encoding", "Upgrade", "Set-Cookie",
}

// FilterResponseHeaders returns the response headers that reach the client: all
// but the hop-by-hop headers, set-cookie and the headers that start with proxy-.
func FilterResponseHeaders(in http.Header) http.Header {
	out := http.Header{}
	for name, values := range in {
		canonical := http.CanonicalHeaderKey(name)
		if slices.Contains(hopByHop, canonical) || strings.HasPrefix(canonical, "Proxy-") {
			continue
		}
		out[canonical] = slices.Clone(values)
	}
	return out
}
