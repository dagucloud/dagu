// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

const (
	cardPath        = "/.well-known/agent-card.json"
	protocolVersion = "1.0"
	bindingJSONRPC  = "JSONRPC"
)

// endpoint is the JSON-RPC interface selected from an Agent Card.
type endpoint struct {
	url    string
	tenant string
}

// cardURL returns where the Agent Card of agentURL is served.
func cardURL(agentURL string) (string, error) {
	u, err := url.Parse(agentURL)
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	if !strings.HasSuffix(u.Path, ".json") {
		// JoinPath keeps an escaped path such as team%2Fdiag intact.
		u = u.JoinPath(".well-known", "agent-card.json")
	}
	return u.String(), nil
}

// parseCard rejects cards that are not A2A 1.0 before decoding them.
func parseCard(body []byte) (*a2a.AgentCard, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if _, ok := probe["supportedInterfaces"]; !ok {
		if _, legacy := probe["url"]; legacy {
			return nil, errors.New("card has no supportedInterfaces and looks like an A2A 0.3 card; only protocol 1.0 is supported")
		}
		return nil, errors.New("card has no supportedInterfaces")
	}
	return agentcard.DefaultCardParser(body)
}

// selectEndpoint picks the first advertised JSON-RPC 1.0 interface.
func selectEndpoint(card *a2a.AgentCard) (endpoint, error) {
	var bindings, versions []string
	for _, in := range card.SupportedInterfaces {
		if in == nil {
			continue
		}
		bindings = append(bindings, string(in.ProtocolBinding))
		if !strings.EqualFold(string(in.ProtocolBinding), bindingJSONRPC) {
			continue
		}
		if v := string(in.ProtocolVersion); v != protocolVersion && !strings.HasPrefix(v, protocolVersion+".") {
			versions = append(versions, v)
			continue
		}
		u, err := url.Parse(in.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return endpoint{}, fmt.Errorf("card advertises an invalid JSON-RPC URL; it must be an absolute https URL without credentials")
		}
		return endpoint{url: in.URL, tenant: in.Tenant}, nil
	}
	if len(versions) > 0 {
		return endpoint{}, fmt.Errorf("card advertises JSONRPC only for unsupported protocol versions %v; only %s is supported", versions, protocolVersion)
	}
	return endpoint{}, fmt.Errorf("card has no JSONRPC interface (advertised bindings: %v)", bindings)
}

// session is a resolved agent, ready for calls.
type session struct {
	transport a2aclient.Transport
	route     route
	// obs observes the JSON-RPC requests only; the card has its own observer.
	obs *observer
}

// connect reads the Agent Card and opens the JSON-RPC transport. Credentials
// go to the agent_url origin only: a card that advertises another origin is
// refused when credentials are configured.
func connect(ctx context.Context, base http.RoundTripper, c common, cardObs *observer) (*session, error) {
	agent, err := url.Parse(c.AgentURL)
	if err != nil {
		return nil, &callError{kind: kindDiscovery, message: "invalid agent_url"}
	}
	g := &guard{base: base, origin: originOf(agent), headers: c.Headers, obs: cardObs}
	cu, err := cardURL(c.AgentURL)
	if err != nil {
		return nil, &callError{kind: kindDiscovery, message: "invalid agent_url"}
	}
	resolver := &agentcard.Resolver{Client: g.client(), CardParser: parseCard}
	card, err := resolver.Resolve(ctx, cu)
	if err != nil {
		return nil, fmt.Errorf("agent card: %w", err)
	}
	ep, err := selectEndpoint(card)
	if err != nil {
		return nil, &callError{kind: kindDiscovery, message: err.Error()}
	}
	rpc, _ := url.Parse(ep.url)
	if originOf(rpc) != g.origin && len(c.Headers) > 0 {
		return nil, &callError{kind: kindDiscovery, message: fmt.Sprintf(
			"card advertises JSON-RPC endpoint on another origin (%s); credentials are never sent there. Use an agent_url on that origin or remove with.headers", originOf(rpc))}
	}
	rpcObs := &observer{}
	rpcGuard := &guard{base: base, origin: g.origin, headers: c.Headers, obs: rpcObs}
	return &session{
		obs:       rpcObs,
		transport: a2aclient.NewJSONRPCTransport(ep.url, rpcGuard.client()),
		route:     route{agentURL: c.AgentURL, tenant: tenantOf(c.Tenant, ep)},
	}, nil
}

func tenantOf(override string, ep endpoint) string {
	if override != "" {
		return override
	}
	return ep.tenant
}

func (s *session) params() a2aclient.ServiceParams {
	return a2aclient.ServiceParams{a2a.SvcParamVersion: {protocolVersion}}
}
