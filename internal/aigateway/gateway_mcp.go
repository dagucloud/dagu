// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// Spec 095, "The MCP relay". The gateway carries the MCP traffic of a step to a
// MCP server entry. It checks the use rule, adds the credentials of the entry,
// checks the key allow on each tools/call, and streams the response. The traffic
// uses the same limits, the same stream rules and the same audit line as the
// model traffic.

// checkMCP is the start check of a MCP server entry: it exists in a scope that the
// DAG sees, and the DAG may use it. It reads no credential value.
func (g *Gateway) checkMCP(ctx context.Context, sub Subject, name string) *Failure {
	if g.mcp == nil {
		return Fail(ReasonEndpointUnavailable, "this process has no MCP server store")
	}
	if _, err := g.mcp.ResolveMCPServer(ctx, name, sub.DAGName, sub.Workspace); err != nil {
		return FailureOf(err)
	}
	return nil
}

// doMCP carries one request to a MCP server. The caller made the identity checks.
// The entry is resolved again for each request, so a deleted entry, a new allow
// list or new credentials apply to the next request.
func (g *Gateway) doMCP(ctx context.Context, req Request, sink ResponseSink) (failure *Failure) {
	start := time.Now()
	record := AuditRecord{
		RunID: req.RunID, DAGName: req.DAGName, StepName: req.StepName, WorkerID: req.WorkerID,
		TargetKind: "mcp", Name: req.Target.Name, Method: req.Method, Route: RouteMCP, started: start,
	}
	defer func() {
		record.DurationMs = time.Since(start).Milliseconds()
		if failure != nil {
			record.Reason = failure.Reason
			if record.Status == 0 {
				record.Status = failure.Reason.Status()
			}
		}
		g.writeAudit(ctx, record)
	}()

	if g.mcp == nil {
		return Fail(ReasonEndpointUnavailable, "this process has no MCP server store")
	}
	server, err := g.mcp.ResolveMCPServer(ctx, req.Target.Name, req.DAGName, req.Workspace)
	if err != nil {
		return FailureOf(err)
	}
	record.Scope = server.Scope.String()
	record.Fingerprint = mcpFingerprint(server)

	name, failure := AllowMCP(req.Method, req.Path, req.RawQuery)
	if failure != nil {
		return failure
	}
	if name != req.Target.Name {
		return Fail(ReasonMCPServerForbidden, "the path names the MCP server %q, and the stream names %q", name, req.Target.Name)
	}
	header, headerBytes, failure := FilterMCPRequestHeaders(req.Header)
	if failure != nil {
		return failure
	}
	if headerBytes > int(g.limits.MaxHeaderBytes) {
		return Fail(ReasonRequestTooLarge, "the request headers have more than %d bytes", g.limits.MaxHeaderBytes)
	}
	if req.ContentLength > g.limits.MaxRequestBytes {
		return Fail(ReasonRequestTooLarge, "the request body has more than %d bytes", g.limits.MaxRequestBytes)
	}

	release, failure := g.streams.acquire(req.AttemptKey)
	if failure != nil {
		return failure
	}
	defer release()

	// The total timeout includes the read of the body.
	callCtx, cancel := context.WithTimeout(ctx, g.limits.TotalTimeout)
	defer cancel()

	// The whole body is read before the call: the coordinator parses a POST body,
	// and the call has a content-length.
	body, failure := g.readBody(ctx, callCtx, req.Body)
	if failure != nil {
		return failure
	}
	record.RequestBytes = int64(len(body))

	if req.Method == http.MethodPost {
		inspection, failure := inspectMCPBody(body, req.MCPAllow)
		if failure != nil {
			return failure
		}
		record.RPCMethod = joinAudit(inspection.methods)
		record.Tool = joinAudit(inspection.tools)
		if inspection.refused != nil {
			return g.refuseTool(ctx, sink, req.Target.Name, inspection, &record)
		}
	}

	credentials, err := g.mcp.ReadCredentials(callCtx, server)
	if err != nil {
		return FailureOf(err)
	}
	httpReq, failure := buildMCPRequest(callCtx, server, req.Method, header, body, credentials)
	if failure != nil {
		return failure
	}
	return g.roundTrip(ctx, callCtx, cancel, httpReq, sink, &record)
}

// refuseTool answers a body that holds a tools/call for a tool outside allow. The
// answer is a JSON-RPC error with the status 200, and no call goes to the server.
// A body with no request, which has no answer, gets the status 202.
func (g *Gateway) refuseTool(ctx context.Context, sink ResponseSink, server string, inspection *mcpInspection, record *AuditRecord) *Failure {
	record.Reason = ReasonMCPToolNotAllowed
	answer := inspection.refusalBody(server)
	header := http.Header{}
	status := http.StatusOK
	if answer == nil {
		status = http.StatusAccepted
	} else {
		header.Set("Content-Type", "application/json")
	}
	record.Status = status
	if err := sink.Head(ctx, status, header); err != nil {
		return g.classify(ctx, ctx, err)
	}
	if answer != nil {
		if err := sink.Write(ctx, answer); err != nil {
			return g.classify(ctx, ctx, err)
		}
		record.ResponseBytes = int64(len(answer))
	}
	return nil
}

// buildMCPRequest builds the call to the URL of the entry, with the headers of the
// allowed list and the credentials of the entry.
func buildMCPRequest(ctx context.Context, server aiendpoint.MCPServer, method string, header http.Header, body []byte, credentials aiendpoint.MCPCredentials) (*http.Request, *Failure) {
	var reader io.Reader
	if method == http.MethodPost {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, server.URL, reader)
	if err != nil {
		return nil, Fail(ReasonUpstreamError, "the call to the MCP server cannot be built")
	}
	httpReq.Header = header.Clone()
	httpReq.Header.Set("Accept-Encoding", "identity")
	credentials.Apply(httpReq.Header)
	return httpReq, nil
}

// mcpFingerprint returns the fingerprints of the credentials of an entry for the
// audit line: the token, then the headers by name. It is empty for an entry with
// no credential.
func mcpFingerprint(server aiendpoint.MCPServer) string {
	var parts []string
	if server.Token != nil {
		parts = append(parts, server.Token.Fingerprint)
	}
	for _, header := range server.Headers {
		parts = append(parts, header.Header+":"+header.Fingerprint)
	}
	return strings.Join(parts, ",")
}
