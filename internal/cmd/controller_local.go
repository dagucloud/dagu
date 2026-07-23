// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os/user"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/controller"
	persisfile "github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/controllerapi"
)

type localControllerCommandClient struct {
	service *controller.Service
	config  *config.Config
}

func newLocalControllerCommandClient(ctx *Context) controllerCommandClient {
	stores := controller.NewFileStores(ctx.Config.Paths.DataDir)
	validator := controller.NewValidator(controller.NewDAGStoreResolver(ctx.DAGStore))
	return &localControllerCommandClient{
		service: controller.NewService(stores.Definitions, stores.Runtimes, stores.Locker, validator),
		config:  ctx.Config,
	}
}

func (c *localControllerCommandClient) listControllers(ctx context.Context) ([]api.ControllerSummary, error) {
	items, err := c.service.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]api.ControllerSummary, 0, len(items))
	for _, item := range items {
		result = append(result, controllerapi.Summary(item))
	}
	return result, nil
}

func (c *localControllerCommandClient) getController(ctx context.Context, id string) (*api.ControllerDetail, error) {
	detail, err := c.service.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, errors.New("controller service returned no detail")
	}
	result := controllerapi.Detail(*detail)
	return &result, nil
}

func (c *localControllerCommandClient) startController(ctx context.Context, id, prompt string) error {
	runtime, err := c.service.Start(ctx, id, prompt)
	if err == nil {
		c.logMutation(ctx, "start", id, runtime.Workspace)
	}
	return err
}

func (c *localControllerCommandClient) promptController(ctx context.Context, id, prompt string) error {
	runtime, err := c.service.Prompt(ctx, id, prompt)
	if err == nil {
		c.logMutation(ctx, "prompt", id, runtime.Workspace)
	}
	return err
}

func (c *localControllerCommandClient) stopController(ctx context.Context, id string) error {
	runtime, err := c.service.Stop(ctx, id)
	if err == nil {
		c.logMutation(ctx, "stop", id, runtime.Workspace)
	}
	return err
}

func (c *localControllerCommandClient) logMutation(ctx context.Context, action, id, workspace string) {
	store, err := persisfile.NewAuditStore(c.config)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize local Controller audit store", tag.Error(err))
		return
	}
	if store == nil {
		return
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn(ctx, "Failed to close local Controller audit store", tag.Error(err))
		}
	}()

	var userID, username string
	if actor, err := user.Current(); err == nil {
		userID = actor.Uid
		username = actor.Username
	}
	details, _ := json.Marshal(map[string]any{
		"controller_id": id,
		"resource_type": "controller",
		"resource_id":   id,
		"workspace":     workspace,
	})
	entry := audit.NewEntry(audit.Category("controller"), action, userID, username).WithDetails(string(details))
	entry.Source = "cli"
	entry.Result = "success"
	entry.ResourceType = "controller"
	entry.ResourceID = id
	entry.Workspace = workspace
	if err := audit.New(store).Log(ctx, entry); err != nil {
		logger.Warn(ctx, "Failed to write local Controller audit log", tag.Error(err))
	}
}
