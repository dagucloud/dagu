// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// CreateInventory stores a new inventory from its record.
func (a *API) CreateInventory(ctx context.Context, request api.CreateInventoryRequestObject) (api.CreateInventoryResponseObject, error) {
	if err := a.requireInventoryWrite(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.CreateInventory400JSONResponse(changeRefused("Request body is required")), nil
	}
	inv, refused, err := a.decodeInventoryChange(ctx, *request.Body)
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return api.CreateInventory400JSONResponse(*refused), nil
	}
	stored, err := a.inventoryStore.Create(ctx, inv)
	if errors.Is(err, inventory.ErrInvalid) {
		return api.CreateInventory400JSONResponse(ruleViolation(err)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create inventory %q: %w", inv.Name, err)
	}
	a.logAudit(ctx, audit.CategorySystem, "inventory_create", map[string]any{"id": stored.ID, "name": stored.Name})
	return api.CreateInventory201JSONResponse(toInventory(stored)), nil
}

// ReplaceInventory replaces an inventory as a whole, given the version the
// change is based on.
func (a *API) ReplaceInventory(ctx context.Context, request api.ReplaceInventoryRequestObject) (api.ReplaceInventoryResponseObject, error) {
	if err := a.requireInventoryWrite(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.ReplaceInventory400JSONResponse(changeRefused("Request body is required")), nil
	}
	inv, refused, err := a.decodeInventoryChange(ctx, *request.Body)
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return api.ReplaceInventory400JSONResponse(*refused), nil
	}
	if inv.ID != "" && inv.ID != request.InventoryId {
		return api.ReplaceInventory400JSONResponse(changeRefused(fmt.Sprintf("the record's id %q is not the inventory's ID %q", inv.ID, request.InventoryId))), nil
	}
	inv.ID = request.InventoryId
	stored, err := a.inventoryStore.Replace(ctx, inv, request.Params.Version)
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		return api.ReplaceInventory400JSONResponse(ruleViolation(err)), nil
	case errors.Is(err, inventory.ErrNotFound):
		return api.ReplaceInventory404JSONResponse(inventoryNotFound(request.InventoryId)), nil
	case errors.Is(err, inventory.ErrStale):
		conflict, found, err := a.inventoryConflict(ctx, request.InventoryId, err)
		if !found {
			return api.ReplaceInventory404JSONResponse(inventoryNotFound(request.InventoryId)), err
		}
		return api.ReplaceInventory409JSONResponse(conflict), err
	case err != nil:
		return nil, fmt.Errorf("failed to replace inventory %q: %w", request.InventoryId, err)
	}
	a.logAudit(ctx, audit.CategorySystem, "inventory_replace", map[string]any{"id": stored.ID, "name": stored.Name})
	return api.ReplaceInventory200JSONResponse(toInventory(stored)), nil
}

// DeleteInventory deletes an inventory, given the version the change is
// based on.
func (a *API) DeleteInventory(ctx context.Context, request api.DeleteInventoryRequestObject) (api.DeleteInventoryResponseObject, error) {
	if err := a.requireInventoryWrite(ctx); err != nil {
		return nil, err
	}
	current, err := a.inventoryStore.Get(ctx, request.InventoryId)
	if errors.Is(err, inventory.ErrNotFound) {
		return api.DeleteInventory404JSONResponse(inventoryNotFound(request.InventoryId)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory %q: %w", request.InventoryId, err)
	}
	err = a.inventoryStore.Delete(ctx, request.InventoryId, request.Params.Version)
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return api.DeleteInventory404JSONResponse(inventoryNotFound(request.InventoryId)), nil
	case errors.Is(err, inventory.ErrStale):
		conflict, found, err := a.inventoryConflict(ctx, request.InventoryId, err)
		if !found {
			return api.DeleteInventory404JSONResponse(inventoryNotFound(request.InventoryId)), err
		}
		return api.DeleteInventory409JSONResponse(conflict), err
	case err != nil:
		return nil, fmt.Errorf("failed to delete inventory %q: %w", request.InventoryId, err)
	}
	a.logAudit(ctx, audit.CategorySystem, "inventory_delete", map[string]any{"id": current.ID, "name": current.Name})
	return api.DeleteInventory204Response{}, nil
}

// PutInventoryNode adds a node to an inventory, or replaces the node of
// that name, given the version the change is based on.
func (a *API) PutInventoryNode(ctx context.Context, request api.PutInventoryNodeRequestObject) (api.PutInventoryNodeResponseObject, error) {
	if err := a.requireInventoryWrite(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.PutInventoryNode400JSONResponse(changeRefused("Request body is required")), nil
	}
	raw, err := rawInventoryBody(ctx, *request.Body)
	if err != nil {
		return nil, err
	}
	node, err := inventory.DecodeNode(raw)
	if err != nil {
		return api.PutInventoryNode400JSONResponse(changeRefused(err.Error())), nil
	}
	switch {
	case node.Name == "":
		node.Name = request.NodeName
	case !strings.EqualFold(node.Name, request.NodeName):
		return api.PutInventoryNode400JSONResponse(changeRefused(fmt.Sprintf("the record's name %q is not the node name %q of the path", node.Name, request.NodeName))), nil
	}

	stored, added, err := inventory.PutNode(ctx, a.inventoryStore, request.InventoryId, node, request.Params.Version)
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		return api.PutInventoryNode400JSONResponse(ruleViolation(err)), nil
	case errors.Is(err, inventory.ErrNotFound):
		return api.PutInventoryNode404JSONResponse(inventoryNotFound(request.InventoryId)), nil
	case errors.Is(err, inventory.ErrStale):
		conflict, found, err := a.inventoryConflict(ctx, request.InventoryId, err)
		if !found {
			return api.PutInventoryNode404JSONResponse(inventoryNotFound(request.InventoryId)), err
		}
		return api.PutInventoryNode409JSONResponse(conflict), err
	case err != nil:
		return nil, fmt.Errorf("failed to change node %q: %w", node.Name, err)
	}
	a.logAudit(ctx, audit.CategorySystem, "inventory_node_put", map[string]any{
		"inventory": stored.Name, "node": node.Name, "added": added,
	})
	if added {
		return api.PutInventoryNode201JSONResponse(toInventory(stored)), nil
	}
	return api.PutInventoryNode200JSONResponse(toInventory(stored)), nil
}

// DeleteInventoryNode removes a node from an inventory, given the version
// the change is based on.
func (a *API) DeleteInventoryNode(ctx context.Context, request api.DeleteInventoryNodeRequestObject) (api.DeleteInventoryNodeResponseObject, error) {
	if err := a.requireInventoryWrite(ctx); err != nil {
		return nil, err
	}
	stored, err := inventory.DeleteNode(ctx, a.inventoryStore, request.InventoryId, request.NodeName, request.Params.Version)
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return api.DeleteInventoryNode404JSONResponse(inventoryNotFound(request.InventoryId)), nil
	case errors.Is(err, inventory.ErrNodeNotFound):
		return api.DeleteInventoryNode404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
	case errors.Is(err, inventory.ErrInvalid):
		// Another node names the removed one as its bastion.
		return api.DeleteInventoryNode400JSONResponse(ruleViolation(err)), nil
	case errors.Is(err, inventory.ErrStale):
		conflict, found, err := a.inventoryConflict(ctx, request.InventoryId, err)
		if !found {
			return api.DeleteInventoryNode404JSONResponse(inventoryNotFound(request.InventoryId)), err
		}
		return api.DeleteInventoryNode409JSONResponse(conflict), err
	case err != nil:
		return nil, fmt.Errorf("failed to remove node %q: %w", request.NodeName, err)
	}
	a.logAudit(ctx, audit.CategorySystem, "inventory_node_delete", map[string]any{
		"inventory": stored.Name, "node": request.NodeName,
	})
	return api.DeleteInventoryNode200JSONResponse(toInventory(stored)), nil
}

// requireInventoryWrite checks that inventories can be changed by the
// caller: an inventory decides which hosts receive the SSH credentials, so a
// change needs the role that edits base config.
func (a *API) requireInventoryWrite(ctx context.Context) error {
	if a.inventoryStore == nil {
		return inventoryStoreUnavailable()
	}
	return a.requireDeveloperOrAbove(ctx)
}

// decodeInventoryChange reads the inventory record of a create or replace.
// refused is set when the body is not a valid record.
func (a *API) decodeInventoryChange(ctx context.Context, body any) (inventory.Inventory, *api.InventoryImportError, error) {
	raw, err := rawInventoryBody(ctx, body)
	if err != nil {
		return inventory.Inventory{}, nil, err
	}
	inv, err := inventory.Decode(raw)
	if err != nil {
		refused := changeRefused(err.Error())
		return inventory.Inventory{}, &refused, nil
	}
	return inv, nil, nil
}

// inventoryConflict answers a change based on an old version with the
// inventory as it is now. found is false when the inventory was deleted
// since.
func (a *API) inventoryConflict(ctx context.Context, id string, stale error) (api.InventoryConflict, bool, error) {
	current, err := a.inventoryStore.Get(ctx, id)
	if errors.Is(err, inventory.ErrNotFound) {
		return api.InventoryConflict{}, false, nil
	}
	if err != nil {
		return api.InventoryConflict{}, true, fmt.Errorf("failed to read inventory %q: %w", id, err)
	}
	return api.InventoryConflict{
		Code:      api.ErrorCodeConflict,
		Message:   stale.Error(),
		Inventory: toInventory(current),
	}, true, nil
}

func ruleViolation(err error) api.InventoryImportError {
	ruleErrs := appendRuleErrors(nil, err)
	rules := "rules"
	if len(ruleErrs) == 1 {
		rules = "rule"
	}
	return api.InventoryImportError{
		Code:    api.ErrorCodeBadRequest,
		Message: fmt.Sprintf("The change breaks %d %s, and nothing was stored", len(ruleErrs), rules),
		Errors:  ruleErrs,
	}
}

func changeRefused(message string) api.InventoryImportError {
	return api.InventoryImportError{Code: api.ErrorCodeBadRequest, Message: message, Errors: []api.InventoryRuleError{}}
}

func inventoryNotFound(id string) api.Error {
	return api.Error{Code: api.ErrorCodeNotFound, Message: fmt.Sprintf("no inventory has the ID %q", id)}
}

// isInventoryBodyRequest matches the requests whose body is an inventory
// record, a node record, or an import.
func isInventoryBodyRequest(r *http.Request, base string) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, base+"/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	switch r.Method {
	case http.MethodPost:
		return rest == "inventory-imports" || rest == "inventories"
	case http.MethodPut:
		return parts[0] == "inventories" && (len(parts) == 2 || (len(parts) == 4 && parts[2] == "nodes"))
	default:
		return false
	}
}
