// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
)

// listNodes returns one page of the inventory nodes a validated nodes query
// selects, as GET /nodes returns them.
func (svc *Service) listNodes(ctx context.Context, rawQuery string) (map[string]any, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	// Values are trimmed, as validateReadQuery trims them before it checks
	// them.
	text := func(key string) *string {
		if !values.Has(key) {
			return nil
		}
		value := strings.TrimSpace(values.Get(key))
		return &value
	}
	// Numbers were range-checked, so a value that does not parse is absent.
	number := func(key string) *int {
		n, err := strconv.Atoi(strings.TrimSpace(values.Get(key)))
		if err != nil {
			return nil
		}
		return &n
	}
	params := daguapi.ListInventoryNodesParams{
		Labels:    text("labels"),
		Inventory: text("inventory"),
		Name:      text("name"),
		Page:      number("page"),
		PerPage:   number("perPage"),
	}

	resp, err := svc.api.ListInventoryNodes(ctx, daguapi.ListInventoryNodesRequestObject{Params: params})
	if err != nil {
		return nil, err
	}
	switch r := resp.(type) {
	case daguapi.ListInventoryNodes200JSONResponse:
		return map[string]any{"items": r.Nodes, "pagination": r.Pagination}, nil
	case daguapi.ListInventoryNodes400JSONResponse:
		// An invalid selector or an unknown inventory.
		return nil, invalidTargetValue(readTargetNodes, readFieldQuery, r.Message)
	default:
		return nil, fmt.Errorf("unexpected inventory node list response %T", resp)
	}
}

// getNode returns the inventory node with the given name, compared without
// case.
func (svc *Service) getNode(ctx context.Context, name string) (daguapi.InventoryNode, error) {
	resp, err := svc.api.GetInventoryNode(ctx, daguapi.GetInventoryNodeRequestObject{NodeName: name})
	if err != nil {
		return daguapi.InventoryNode{}, err
	}
	switch r := resp.(type) {
	case daguapi.GetInventoryNode200JSONResponse:
		return daguapi.InventoryNode(r), nil
	case daguapi.GetInventoryNode404JSONResponse:
		return daguapi.InventoryNode{}, resourceNotFoundReadError(readInput{Target: readTargetNode}, fmt.Sprintf("node %q not found", name))
	default:
		return daguapi.InventoryNode{}, fmt.Errorf("unexpected inventory node response %T", resp)
	}
}

// listInventories returns every inventory without its nodes.
func (svc *Service) listInventories(ctx context.Context) (map[string]any, error) {
	resp, err := svc.api.ListInventories(ctx, daguapi.ListInventoriesRequestObject{})
	if err != nil {
		return nil, err
	}
	r, ok := resp.(daguapi.ListInventories200JSONResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected inventory list response %T", resp)
	}
	return map[string]any{"items": r.Inventories}, nil
}
