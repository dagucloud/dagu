// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// BoundNode is the inventory node a bound run reads as ${node.*} (RFC 013
// §The node namespace).
type BoundNode struct {
	Name        string
	Host        string
	Port        string
	User        string
	Description string
	// Labels maps each label key to its value, which is empty for a label
	// with no value.
	Labels map[string]string
}

var (
	// nodeLabelKeyPattern matches a label key written unquoted in
	// ${node.labels.<key>}. A key with a dot is quoted.
	nodeLabelKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	// quotedNodeLabelKeyPattern matches any label key, as written quoted.
	quotedNodeLabelKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

// parseNodePath splits a path of the node namespace into its field and, for
// one label, the label key: node.name, node.labels, node.labels.<key>, or
// node.labels."<key>".
func parseNodePath(path string) (field, label string, ok bool) {
	rest, ok := strings.CutPrefix(path, "node.")
	if !ok {
		return "", "", false
	}
	switch rest {
	case "name", "host", "port", "user", "description", "labels":
		return rest, "", true
	}
	key, ok := strings.CutPrefix(rest, "labels.")
	if !ok {
		return "", "", false
	}
	if !strings.HasPrefix(key, `"`) {
		return "labels", key, nodeLabelKeyPattern.MatchString(key)
	}
	var unquoted string
	if err := json.Unmarshal([]byte(key), &unquoted); err != nil || !quotedNodeLabelKeyPattern.MatchString(unquoted) {
		return "", "", false
	}
	return "labels", unquoted, true
}

// readsNodeVariable reports whether a node path reads a variable named node
// instead, as ${node.<path>} did before the node namespace: outside a bound
// run, when the scope holds such a variable.
func readsNodeVariable(segments []string, scope RuntimeScope) bool {
	if segments[0] != "node" || scope.Node != nil {
		return false
	}
	if _, ok := scope.Params["node"]; ok {
		return true
	}
	_, ok := scope.Env.GetEntry("node")
	return ok
}

func bindingNodeValue(path string, node *BoundNode, requireValue bool) (any, error) {
	if node == nil {
		if !requireValue {
			return nil, nil
		}
		return nil, newNoticeReasonError(
			ValueReferenceReasonNamespaceUnavailable,
			fmt.Sprintf("%s is available only in a run bound to a node", path),
		)
	}
	field, key, _ := parseNodePath(path)
	switch field {
	case "name":
		return node.Name, nil
	case "host":
		return node.Host, nil
	case "port":
		return node.Port, nil
	case "user":
		return node.User, nil
	case "description":
		return node.Description, nil
	}
	if key == "" {
		return node.labelsText(), nil
	}
	value, ok := node.Labels[strings.ToLower(key)]
	if !ok {
		return nil, missingNodeLabel(node.Name, key)
	}
	return value, nil
}

// labelsText returns the labels sorted and comma-separated, as key or
// key=value.
func (n *BoundNode) labelsText() string {
	labels := make([]string, 0, len(n.Labels))
	for key, value := range n.Labels {
		if value == "" {
			labels = append(labels, key)
		} else {
			labels = append(labels, key+"="+value)
		}
	}
	slices.Sort(labels)
	return strings.Join(labels, ",")
}

// CheckNodeReferences returns an error for the first ${node.<path>} in raw,
// read as a value of the field, that node cannot give: a path the namespace
// does not define, or a label node lacks. A bound run fails on it before any
// step starts, since the text would otherwise reach the node unresolved.
func (f Field) CheckNodeReferences(raw string, node *BoundNode) error {
	for _, ref := range scanReferences(raw, f.escapesDollar()) {
		if !ref.Braced || len(ref.Segments) < 2 || ref.Namespace != "node" {
			continue
		}
		_, key, ok := parseNodePath(ref.Expr)
		switch {
		case !ok && strings.HasPrefix(ref.Expr, "node.labels."):
			return fmt.Errorf(`%s names no node label; quote a key with a dot, as ${node.labels."gpu.type"}`, ref.Raw)
		case !ok:
			return fmt.Errorf(`%s names no node field: name, host, port, user, description, or labels; escape it as \%s to keep the text`, ref.Raw, ref.Raw)
		case key != "":
			if _, found := node.Labels[strings.ToLower(key)]; !found {
				return missingNodeLabel(node.Name, key)
			}
		}
	}
	return nil
}

func missingNodeLabel(node, key string) error {
	return fmt.Errorf("node %q has no label %q", node, key)
}
