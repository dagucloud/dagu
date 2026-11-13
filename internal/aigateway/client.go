// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// StepRef names the step that uses the gateway. The step takes the values from its
// own run.
type StepRef struct {
	RunID     string
	DAGName   string
	Workspace string
	StepName  string
	// MCPAllow maps the name of each named MCP server of the step to its key allow
	// (Spec 095, "The key allow"). Only the local mode reads it: the process that
	// runs the step holds the DAG. The coordinator never reads it from a worker. It
	// takes the lists from the DAG that it holds.
	MCPAllow map[string][]string
}

// TargetKind is the kind of a target of a relay request.
type TargetKind string

// The kinds of target of Spec 094, "Terms". The values are the values of the field
// kind of the message RelayTarget.
const (
	TargetEndpoint TargetKind = "endpoint"
	TargetMCP      TargetKind = "mcp"
)

// Target is an endpoint or a MCP server entry that a relay request names.
type Target struct {
	Kind TargetKind
	Name string
}

// EndpointTarget returns the target of an endpoint.
func EndpointTarget(name string) Target { return Target{Kind: TargetEndpoint, Name: name} }

// MCPTarget returns the target of a MCP server entry.
func MCPTarget(name string) Target { return Target{Kind: TargetMCP, Name: name} }

func (t Target) String() string {
	if t.Kind == TargetMCP {
		return fmt.Sprintf("MCP server %q", t.Name)
	}
	return fmt.Sprintf("endpoint %q", t.Name)
}

// HTTPRequest is the head of a request of a client, as the relay of the worker
// received it.
type HTTPRequest struct {
	Method        string
	Path          string
	RawQuery      string
	Header        http.Header
	ContentLength int64 // -1 when unknown
}

// Client is what the relay of a worker calls. The coordinator client implements it
// with the stream over gRPC. Local implements it with a call to the gateway in
// the same process.
//
// Each method returns nil or a *Failure.
type Client interface {
	// Check is the start check of a step: it makes the checks that need no
	// request (Spec 094, "Start checks").
	Check(ctx context.Context, step StepRef, target Target, provider string) (CheckResult, error)
	// Relay carries one request. It reads the body, calls the sink for the response,
	// and returns when the response ends or the context ends.
	Relay(ctx context.Context, step StepRef, target Target, req HTTPRequest, body io.Reader, sink ResponseSink) error
}

// ModelClient is a Client that also checks the model of a step at start (Spec 093,
// "Use in a step"). Local and the client of the coordinator implement it.
type ModelClient interface {
	Client
	CheckModel(ctx context.Context, step StepRef, target Target, provider, model string) (CheckResult, error)
}

// CheckStart makes the start check of a target, with the model rule when model is
// not empty and the client can check it. The executors call it.
func CheckStart(ctx context.Context, client Client, step StepRef, target Target, provider, model string) (CheckResult, error) {
	if mc, ok := client.(ModelClient); ok && model != "" {
		return mc.CheckModel(ctx, step, target, provider, model)
	}
	return client.Check(ctx, step, target, provider)
}

// Local is the Client of a run with no coordinator (Spec 094, "Local mode"). It
// calls the gateway in the process of the run. It opens no gRPC stream and
// checks no certificate.
type Local struct {
	Gateway *Gateway
}

var _ ModelClient = Local{}

func (l Local) subject(step StepRef) Subject {
	return Subject{
		RunID: step.RunID, DAGName: step.DAGName, Workspace: step.Workspace,
		StepName: step.StepName, WorkerID: "local", AttemptKey: step.RunID,
	}
}

// Check implements Client.
func (l Local) Check(ctx context.Context, step StepRef, target Target, provider string) (CheckResult, error) {
	return l.CheckModel(ctx, step, target, provider, "")
}

// CheckModel implements ModelClient.
func (l Local) CheckModel(ctx context.Context, step StepRef, target Target, provider, model string) (CheckResult, error) {
	result, failure := l.Gateway.CheckModel(ctx, l.subject(step), target, provider, model)
	if failure != nil {
		return CheckResult{}, failure
	}
	return result, nil
}

// Relay implements Client.
func (l Local) Relay(ctx context.Context, step StepRef, target Target, req HTTPRequest, body io.Reader, sink ResponseSink) error {
	failure := l.Gateway.Do(ctx, Request{
		Subject:       l.subject(step),
		Target:        target,
		MCPAllow:      step.MCPAllow[target.Name],
		Method:        req.Method,
		Path:          req.Path,
		RawQuery:      req.RawQuery,
		Header:        req.Header,
		ContentLength: req.ContentLength,
		Body:          body,
	}, sink)
	if failure != nil {
		return failure
	}
	return nil
}

type clientKey struct{}

// WithClient returns a context that carries the client of the gateway. The worker
// sets it for each run, and a local process sets it for its run.
func WithClient(ctx context.Context, client Client) context.Context {
	return context.WithValue(ctx, clientKey{}, client)
}

// ClientFrom returns the client of the gateway that the context carries, or nil.
func ClientFrom(ctx context.Context) Client {
	client, _ := ctx.Value(clientKey{}).(Client)
	return client
}
