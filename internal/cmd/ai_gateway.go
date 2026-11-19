// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// newAIGateway builds the model gateway of Spec 094 from the endpoint store of
// the data directory, the secret store that holds the keys and the block ai of
// the configuration. It serves the endpoints and the MCP server entries. The coordinator serves it to the relays of the workers. A
// local run calls it in its own process. It returns nil when no secret store
// exists, because an endpoint needs one.
func newAIGateway(ctx *Context, secrets secret.Store) (*aigateway.Gateway, error) {
	service := file.NewAIEndpointService(ctx.Context, ctx.backend.Collection(persis.CollectionAIEndpoints), secrets)
	if service == nil {
		return nil, nil
	}
	ai := ctx.Config.AI
	enableAIModels(ctx, service)
	cfg := aigateway.Config{
		Endpoints: service,
		Limits:    aigateway.LimitsFromConfig(ai.Relay),
		ProxyURL:  ai.Upstream.ProxyURL,
	}
	// A MCP server entry is a managed secret too, so it needs the same secret store.
	if servers := file.NewAIMCPServerService(ctx.Context, ctx.backend.Collection(persis.CollectionAIMCPServers), secrets); servers != nil {
		cfg.MCPServers = servers
	}
	gateway, err := aigateway.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create the AI gateway: %w", err)
	}
	return gateway, nil
}

// withLocalAIGateway gives the steps of a local run the gateway of the process
// (Spec 094, "Local mode"). The relay then calls the code of the gateway in the
// same process: no gRPC stream is open and no certificate is checked. A process
// with no secret store has no gateway, and a step that names an endpoint fails
// with the reason relay_unavailable.
func withLocalAIGateway(ctx *Context, secrets secret.Store) (*Context, error) {
	gateway, err := newAIGateway(ctx, secrets)
	if err != nil {
		return ctx, err
	}
	if gateway == nil {
		return ctx, nil
	}
	return ctx.WithContext(aigateway.WithClient(ctx.Context, aigateway.Local{Gateway: gateway})), nil
}

// enableAIModels gives the endpoint service of the gateway the models lists that
// the server keeps in the data directory. The model check of a step reads them,
// and refreshes one list on demand (Spec 093, "Use in a step"). The reader has the
// settings of the server: the proxy of ai.upstream.proxy_url only, and the timeout
// ai.models_timeout_sec. A failure is logged, and the gateway then makes no model
// check.
func enableAIModels(ctx *Context, service *aiendpoint.Service) {
	ai := ctx.Config.AI
	client, err := aigateway.NewUpstreamClient(aigateway.LimitsFromConfig(ai.Relay), ai.Upstream.ProxyURL)
	if err != nil {
		logger.Warn(ctx.Context, "Failed to create the client of the AI models lists", tag.Error(err))
		return
	}
	reader, err := aiendpoint.NewModelsReader(client, time.Duration(ai.ModelsTimeoutSec)*time.Second)
	if err != nil {
		logger.Warn(ctx.Context, "Failed to create the reader of the AI models lists", tag.Error(err))
		return
	}
	file.EnableAIEndpointModels(ctx.Context, service, ctx.backend.Collection(persis.CollectionAIEndpointModels), reader)
}
