// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"net/url"
	"strconv"

	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// Foreach items are addressed under their step: the items collection, one
// item, and a body step log of one item, each for a root or a child run.

const (
	readTargetForeachItems = "foreach_items"
	readTargetForeachItem  = "foreach_item"

	readFieldItem         = "item"
	readFieldBodyStepName = "bodyStepName"

	foreachItemsMaxPerPage = 500
)

// runResource is a parsed dagu://runs/... path below the run itself.
type runResource struct {
	addr         runAddress
	stepName     string
	item         string
	bodyStepName string
	kind         runResourceKind
}

type runResourceKind int

const (
	runResourceOther runResourceKind = iota
	runResourceForeachItems
	runResourceForeachItem
	runResourceForeachStepLog
)

// parseForeachResource recognizes the foreach resource shapes:
//
//	runs/{name}/{dagRunId}[/sub/{subRunId}]/steps/{stepName}/foreach
//	runs/{name}/{dagRunId}[/sub/{subRunId}]/steps/{stepName}/foreach/{item}
//	runs/{name}/{dagRunId}[/sub/{subRunId}]/steps/{stepName}/foreach/{item}/steps/{bodyStepName}/logs
func parseForeachResource(segments []string) (runResource, bool) {
	if len(segments) < 5 {
		return runResource{}, false
	}
	resource := runResource{addr: runAddress{name: segments[0], dagRunID: segments[1]}}
	rest := segments[2:]
	if rest[0] == "sub" {
		if len(rest) < 5 {
			return runResource{}, false
		}
		resource.addr.subRunID = rest[1]
		rest = rest[2:]
	}
	if rest[0] != "steps" || rest[2] != "foreach" {
		return runResource{}, false
	}
	resource.stepName = rest[1]
	switch len(rest) {
	case 3:
		resource.kind = runResourceForeachItems
	case 4:
		resource.kind = runResourceForeachItem
		resource.item = rest[3]
	case 7:
		if rest[4] != "steps" || rest[6] != "logs" {
			return runResource{}, false
		}
		resource.kind = runResourceForeachStepLog
		resource.item = rest[3]
		resource.bodyStepName = rest[5]
	default:
		return runResource{}, false
	}
	return resource, true
}

func (a runAddress) foreachItemsURI(stepName string) string {
	return a.uri() + "/steps/" + pathEscape(stepName) + "/foreach"
}

func (a runAddress) foreachItemURI(stepName, item string) string {
	return a.foreachItemsURI(stepName) + "/" + pathEscape(item)
}

func (a runAddress) foreachStepLogURI(stepName, item, bodyStepName string) string {
	return a.foreachItemURI(stepName, item) + "/steps/" + pathEscape(bodyStepName) + "/logs"
}

// readInput resolves the resource to the tool input it stands for.
func (r runResource) readInput(query string) readInput {
	input := readInput{
		Name:         r.addr.name,
		DAGRunID:     r.addr.dagRunID,
		SubRunID:     r.addr.subRunID,
		StepName:     r.stepName,
		Item:         r.item,
		BodyStepName: r.bodyStepName,
		Query:        query,
	}
	switch r.kind {
	case runResourceForeachItems:
		input.Target = readTargetForeachItems
		input.URI = uriWithQuery(r.addr.foreachItemsURI(r.stepName), query)
	case runResourceForeachItem:
		input.Target = readTargetForeachItem
		input.URI = r.addr.foreachItemURI(r.stepName, r.item)
	case runResourceForeachStepLog:
		input.Target = readTargetStepLog
		input.URI = uriWithQuery(r.addr.foreachStepLogURI(r.stepName, r.item, r.bodyStepName), query)
	case runResourceOther:
	}
	return input
}

// queryTarget is the target whose query rules the resource follows.
func (r runResource) queryTarget() string {
	switch r.kind {
	case runResourceForeachItems:
		return readTargetForeachItems
	case runResourceForeachStepLog:
		return readTargetStepLog
	case runResourceForeachItem, runResourceOther:
		return ""
	}
	return ""
}

func (r runResource) link() resourceLink {
	sub := r.addr.subRunID != ""
	prefix, title := "dag_run_", "DAG-run "
	if sub {
		prefix, title = "sub_dag_run_", "Sub DAG-run "
	}
	switch r.kind {
	case runResourceForeachItem:
		return resourceLink{name: prefix + "foreach_item", title: title + "foreach item", description: "One item of a foreach step with its body steps.", mimeType: resourceMIMEJSON}
	case runResourceForeachStepLog:
		return resourceLink{name: prefix + "foreach_step_log", title: title + "foreach step log", description: "Log output for one body step of a foreach item.", mimeType: resourceMIMEJSON}
	case runResourceForeachItems, runResourceOther:
	}
	return resourceLink{name: prefix + "foreach_items", title: title + "foreach items", description: "Items of a foreach step.", mimeType: resourceMIMEJSON}
}

// foreachItemsQuery maps a validated foreach_items query string onto the
// listing query. Values were already range-checked, so parse failures are
// treated as absent.
func foreachItemsQuery(rawQuery string) frontendapi.ForeachItemsQuery {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return frontendapi.ForeachItemsQuery{}
	}
	intValue := func(key string) int {
		n, err := strconv.Atoi(values.Get(key))
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	return frontendapi.ForeachItemsQuery{
		Parent:  values.Get("parent"),
		Status:  values.Get("status"),
		Page:    intValue("page"),
		PerPage: intValue("perPage"),
	}
}

func validForeachItemsQueryValue(key, value string) bool {
	switch key {
	case "parent":
		return value != ""
	case "status":
		switch value {
		case "not_started", "running", "succeeded", "failed", "aborted":
			return true
		}
	case "page":
		return validIntRange(value, 1, 0)
	case "perPage":
		return validIntRange(value, 1, foreachItemsMaxPerPage)
	}
	return false
}
