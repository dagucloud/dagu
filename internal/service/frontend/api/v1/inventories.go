// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
)

func inventoryStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "Inventory store not configured",
	}
}

// ListInventoryNodes returns the nodes that match the request's selector,
// inventory, and name, in name order, one page at a time: the nodes of the
// inventories and the worker nodes.
func (a *API) ListInventoryNodes(ctx context.Context, request api.ListInventoryNodesRequestObject) (api.ListInventoryNodesResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	params := request.Params

	var filters []ir.LabelFilter
	if selector := strings.TrimSpace(valueOf(params.Labels)); selector != "" {
		var err error
		if filters, err = inventory.ParseSelector(selector); err != nil {
			return api.ListInventoryNodes400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
		}
	}
	var inventoryID string
	if ref := strings.TrimSpace(valueOf(params.Inventory)); ref != "" {
		stored, found, err := a.findInventory(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !found {
			return api.ListInventoryNodes400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: fmt.Sprintf("no inventory has the ID or name %q", ref),
			}, nil
		}
		inventoryID = stored.ID
	}

	nodes, err := a.inventoryStore.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
	}
	workers, conflicts, err := a.listWorkerNodes(ctx, nodes)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(valueOf(params.Name))
	listed := func(nodeName string, labels ir.Labels) bool {
		return labels.MatchesFilters(filters) && strings.Contains(strings.ToLower(nodeName), name)
	}
	var matched []api.InventoryNode
	for _, n := range nodes {
		if (inventoryID == "" || n.InventoryID == inventoryID) && listed(n.Name, n.Labels) {
			matched = append(matched, toInventoryNode(n))
		}
	}
	// A worker node belongs to no inventory.
	if inventoryID == "" {
		for _, w := range workers {
			if listed(w.Name, w.Labels) {
				matched = append(matched, toWorkerNode(w))
			}
		}
		slices.SortFunc(matched, func(a, b api.InventoryNode) int { return strings.Compare(a.Name, b.Name) })
	}

	pg := pagination.NewPaginator(valueOf(params.Page), valueOf(params.PerPage))
	page := pageOf(matched, pg)
	items := append(make([]api.InventoryNode, 0, len(page)), page...)
	return api.ListInventoryNodes200JSONResponse{
		Nodes:           items,
		Pagination:      toPagination(pagination.NewPaginatedResult(items, len(matched), pg)),
		NodeCount:       len(nodes) + len(workers),
		WorkerConflicts: toWorkerConflicts(conflicts),
	}, nil
}

// GetInventoryNode returns one node, named without regard to case.
func (a *API) GetInventoryNode(ctx context.Context, request api.GetInventoryNodeRequestObject) (api.GetInventoryNodeResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	nodes, err := a.inventoryStore.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
	}
	if node, ok := findInventoryNode(nodes, request.NodeName); ok {
		return api.GetInventoryNode200JSONResponse(toInventoryNode(node)), nil
	}
	workers, _, err := a.listWorkerNodes(ctx, nodes)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(workers, func(w inventory.WorkerNode) bool {
		return strings.EqualFold(w.Name, request.NodeName)
	})
	if i < 0 {
		return api.GetInventoryNode404JSONResponse{Code: api.ErrorCodeNotFound, Message: "Node not found"}, nil
	}
	return api.GetInventoryNode200JSONResponse(toWorkerNode(workers[i])), nil
}

// ListInventoryNodeLabels returns the distinct labels of every node, worker
// nodes included, sorted.
func (a *API) ListInventoryNodeLabels(ctx context.Context, _ api.ListInventoryNodeLabelsRequestObject) (api.ListInventoryNodeLabelsResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	nodes, err := a.inventoryStore.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
	}
	workers, _, err := a.listWorkerNodes(ctx, nodes)
	if err != nil {
		return nil, err
	}
	var labels []string
	for _, n := range nodes {
		labels = append(labels, n.Labels.Strings()...)
	}
	for _, w := range workers {
		labels = append(labels, w.Labels.Strings()...)
	}
	slices.Sort(labels)
	return api.ListInventoryNodeLabels200JSONResponse{
		Labels: append([]string{}, slices.Compact(labels)...),
		Errors: []string{},
	}, nil
}

// ListInventories returns every inventory without its nodes, in name order.
func (a *API) ListInventories(ctx context.Context, _ api.ListInventoriesRequestObject) (api.ListInventoriesResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	stored, err := a.inventoryStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list inventories: %w", err)
	}
	summaries := make([]api.InventorySummary, 0, len(stored))
	for _, s := range stored {
		summaries = append(summaries, api.InventorySummary{
			Id:          s.ID,
			Name:        s.Name,
			Description: ptrOf(s.Description),
			NodeCount:   len(s.Nodes),
			Version:     s.Version,
			CreatedAt:   s.CreatedAt,
			UpdatedAt:   s.UpdatedAt,
		})
	}
	return api.ListInventories200JSONResponse{Inventories: summaries}, nil
}

// GetInventory returns one inventory as stored, with its version.
func (a *API) GetInventory(ctx context.Context, request api.GetInventoryRequestObject) (api.GetInventoryResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	stored, err := a.inventoryStore.Get(ctx, request.InventoryId)
	if errors.Is(err, inventory.ErrNotFound) {
		return api.GetInventory404JSONResponse{Code: api.ErrorCodeNotFound, Message: "Inventory not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory: %w", err)
	}
	return api.GetInventory200JSONResponse(toInventory(stored)), nil
}

// ImportInventories checks a set of inventories against every rule and stores
// them, as the store's Import defines. It needs the role that edits base
// config, since an inventory decides which hosts receive the SSH credentials.
func (a *API) ImportInventories(ctx context.Context, request api.ImportInventoriesRequestObject) (api.ImportInventoriesResponseObject, error) {
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return importRefused("Request body is required", nil), nil
	}
	raw, err := rawInventoryBody(ctx, *request.Body)
	if err != nil {
		return nil, err
	}
	invs, err := inventory.DecodeList(raw)
	if err != nil {
		return importRefused(err.Error(), nil), nil
	}

	opts := inventory.ImportOptions{
		Replace: valueOf(request.Params.Replace),
		DryRun:  valueOf(request.Params.DryRun),
	}
	result, err := a.inventoryStore.Import(ctx, invs, opts)
	// An import that fails partway keeps what it stored, so that is audited
	// too.
	if !opts.DryRun && len(result.Created)+len(result.Replaced) > 0 {
		a.logAudit(ctx, audit.CategorySystem, "inventory_import", map[string]any{
			"created":  result.Created,
			"replaced": result.Replaced,
		})
	}
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		ruleErrs := appendRuleErrors(nil, err)
		rules := "rules"
		if len(ruleErrs) == 1 {
			rules = "rule"
		}
		return importRefused(fmt.Sprintf("The import breaks %d %s, and nothing was stored", len(ruleErrs), rules), ruleErrs), nil
	case errors.Is(err, inventory.ErrStale):
		return api.ImportInventories409JSONResponse{
			Code:    api.ErrorCodeConflict,
			Message: "An inventory changed during the import; run it again",
		}, nil
	case err != nil:
		return nil, fmt.Errorf("failed to import inventories: %w", err)
	}
	return api.ImportInventories200JSONResponse{
		Created:  append([]string{}, result.Created...),
		Replaced: append([]string{}, result.Replaced...),
		DryRun:   opts.DryRun,
	}, nil
}

type inventoryBodyKey struct{}

// maxInventoryBodyBytes bounds the body of an inventory import or change,
// which is read whole before the caller's role is checked.
const maxInventoryBodyBytes int64 = 32 << 20

// rawInventoryBody returns the body of an inventory import or change as the
// caller sent it, since the inventory decoder refuses unknown fields and data
// after the value. An in-process caller passes only the decoded JSON, which
// holds every field it sent.
func rawInventoryBody(ctx context.Context, decoded any) ([]byte, error) {
	if raw, ok := ctx.Value(inventoryBodyKey{}).([]byte); ok {
		return raw, nil
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("failed to read the request body: %w", err)
	}
	return raw, nil
}

// inventoryBodyMiddleware keeps the body of an inventory import or change as
// it arrived. The generated handler decodes only the first JSON value and
// drops unknown fields, and these requests must refuse both.
func inventoryBodyMiddleware(mountedAPIPath string) func(http.Handler) http.Handler {
	base := strings.TrimSuffix(mountedAPIPath, "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isInventoryBodyRequest(r, base) {
				next.ServeHTTP(w, r)
				return
			}
			body := http.MaxBytesReader(w, r.Body, maxInventoryBodyBytes)
			raw, err := io.ReadAll(body)
			_ = body.Close()
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				WriteErrorResponse(w, &Error{
					HTTPStatus: http.StatusRequestEntityTooLarge,
					Code:       api.ErrorCodePayloadTooLarge,
					Message:    fmt.Sprintf("an inventory request is at most %d bytes", maxInventoryBodyBytes),
				})
				return
			}
			if err != nil {
				WriteErrorResponse(w, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeBadRequest,
					Message:    "failed to read the request body",
				})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), inventoryBodyKey{}, raw)))
		})
	}
}

// findInventory finds an inventory by its ID, or else by its name compared
// without case.
func (a *API) findInventory(ctx context.Context, ref string) (inventory.Stored, bool, error) {
	stored, err := a.inventoryStore.List(ctx)
	if err != nil {
		return inventory.Stored{}, false, fmt.Errorf("failed to list inventories: %w", err)
	}
	i := slices.IndexFunc(stored, func(s inventory.Stored) bool { return s.ID == ref })
	if i < 0 {
		i = slices.IndexFunc(stored, func(s inventory.Stored) bool { return strings.EqualFold(s.Name, ref) })
	}
	if i < 0 {
		return inventory.Stored{}, false, nil
	}
	return stored[i], true, nil
}

func importRefused(message string, ruleErrs []api.InventoryRuleError) api.ImportInventories400JSONResponse {
	if ruleErrs == nil {
		ruleErrs = []api.InventoryRuleError{}
	}
	return api.ImportInventories400JSONResponse{Code: api.ErrorCodeBadRequest, Message: message, Errors: ruleErrs}
}

// appendRuleErrors appends each broken rule that err holds, in order. err may
// join rule errors, and joins of them, as the inventory store reports them.
func appendRuleErrors(out []api.InventoryRuleError, err error) []api.InventoryRuleError {
	switch e := err.(type) {
	case *inventory.Error:
		return append(out, api.InventoryRuleError{
			Inventory: ptrOf(e.Inventory),
			Node:      ptrOf(e.Node),
			Field:     ptrOf(e.Field),
			Message:   e.Err.Error(),
		})
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			out = appendRuleErrors(out, inner)
		}
		return out
	default:
		return append(out, api.InventoryRuleError{Message: err.Error()})
	}
}

// pageOf returns the items of the page that pg names.
func pageOf[T any](items []T, pg pagination.Paginator) []T {
	start := min(pg.Offset(), len(items))
	end := start + min(pg.Limit(), len(items)-start)
	return items[start:end]
}

// toInventoryNode describes a resolved node. Its credentials are secret refs
// and key paths only: an inventory refuses a literal password or passphrase.
func toInventoryNode(n inventory.ResolvedNode) api.InventoryNode {
	labels := n.Labels.Strings()
	slices.Sort(labels)
	return api.InventoryNode{
		Name:      n.Name,
		Kind:      api.InventoryNodeKindSsh,
		Inventory: &api.InventoryRef{Id: n.InventoryID, Name: n.InventoryName},
		Host:      n.Host,
		Port:      new(n.Port),
		User:      ptrOf(n.User),
		Auth: &api.InventoryNodeAuth{
			KeyPath:          ptrOf(n.Auth.Key),
			KeySecret:        ptrOf(n.Auth.KeySecret),
			PasswordSecret:   ptrOf(n.Auth.PasswordSecret),
			PassphraseSecret: ptrOf(n.Auth.PassphraseSecret),
		},
		Bastion:       ptrOf(n.Bastion),
		HostKey:       ptrOf(n.HostKey),
		StrictHostKey: new(n.StrictHostKey),
		Shell:         ptrOf(n.Shell),
		Timeout:       new(n.Timeout.String()),
		Labels:        append([]string{}, labels...),
		Description:   ptrOf(n.Description),
		Disabled:      n.Disabled,
	}
}

// toInventory describes a stored inventory in its record's own field names, so
// that the record round-trips through an export and an import unchanged.
func toInventory(s inventory.Stored) api.Inventory {
	nodes := make([]api.InventoryNodeSpec, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		nodes = append(nodes, api.InventoryNodeSpec{
			Name:          n.Name,
			Host:          n.Host,
			Port:          ptrOf(n.Port),
			User:          ptrOf(n.User),
			Key:           credentialOf(n.Key),
			Password:      credentialOf(n.Password),
			Passphrase:    credentialOf(n.Passphrase),
			Bastion:       ptrOf(n.Bastion),
			HostKey:       ptrOf(n.HostKey),
			StrictHostKey: n.StrictHostKey,
			Shell:         ptrOf(n.Shell),
			Timeout:       ptrOf(n.Timeout),
			Labels:        ptrOf(n.Labels),
			Description:   ptrOf(n.Description),
			Disabled:      ptrOf(n.Disabled),
		})
	}
	d := s.Defaults
	return api.Inventory{
		Id:          s.ID,
		Name:        s.Name,
		Description: ptrOf(s.Description),
		Defaults: api.InventoryDefaults{
			Port:          ptrOf(d.Port),
			User:          ptrOf(d.User),
			Key:           credentialOf(d.Key),
			Password:      credentialOf(d.Password),
			Passphrase:    credentialOf(d.Passphrase),
			Bastion:       ptrOf(d.Bastion),
			HostKey:       ptrOf(d.HostKey),
			StrictHostKey: d.StrictHostKey,
			Shell:         ptrOf(d.Shell),
			Timeout:       ptrOf(d.Timeout),
			Labels:        ptrOf(d.Labels),
		},
		Nodes:     nodes,
		Version:   s.Version,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func credentialOf(v any) *api.InventoryCredential {
	if v == nil {
		return nil
	}
	return &v
}
