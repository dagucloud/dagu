// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var topicTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// dagHistoryWithBackfill is the one query a DAG history topic takes: it
// lists the DAG's backfill runs too.
const dagHistoryWithBackfill = "includeBackfill=true"

// ParsedTopic is the canonical representation of a topic string.
type ParsedTopic struct {
	Type       TopicType
	Identifier string
	Key        string
}

// ParseTopic validates and canonicalizes a raw topic string.
func ParseTopic(raw string) (ParsedTopic, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedTopic{}, fmt.Errorf("topic must not be empty")
	}

	topicType, identifier, ok := strings.Cut(raw, ":")
	if !ok || topicType == "" {
		return ParsedTopic{}, fmt.Errorf("invalid topic format: %q", raw)
	}
	if !topicTypePattern.MatchString(topicType) {
		return ParsedTopic{}, fmt.Errorf("invalid topic type: %q", topicType)
	}

	canonicalIdentifier, err := canonicalizeTopicIdentifier(TopicType(topicType), identifier)
	if err != nil {
		return ParsedTopic{}, err
	}

	return ParsedTopic{
		Type:       TopicType(topicType),
		Identifier: canonicalIdentifier,
		Key:        string(TopicType(topicType)) + ":" + canonicalIdentifier,
	}, nil
}

func canonicalizeTopicIdentifier(topicType TopicType, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)

	switch topicType {
	case TopicTypeDAGRuns, TopicTypeDAGRunActivity, TopicTypeQueues, TopicTypeDAGsList, TopicTypeServiceDAGs, TopicTypeWikiTree, TopicTypeLegacyDocTree:
		identifier = strings.TrimPrefix(identifier, "?")
		return parseAndSanitizeQuery(identifier)
	case TopicTypeDAGRunLogs, TopicTypeWikiPage, TopicTypeLegacyDoc:
		pathPart, queryPart, hasQuery := strings.Cut(identifier, "?")
		pathPart = strings.TrimSpace(pathPart)
		if pathPart == "" {
			return "", fmt.Errorf("topic %q requires an identifier", topicType)
		}
		if !hasQuery {
			return pathPart, nil
		}
		queryPart, err := parseAndSanitizeQuery(queryPart)
		if err != nil {
			return "", err
		}
		if queryPart == "" {
			return pathPart, nil
		}
		return pathPart + "?" + queryPart, nil
	case TopicTypeDAG:
		return canonicalizeDAGFile(topicType, identifier)
	case TopicTypeDAGHistory:
		fileName, query, hasQuery := strings.Cut(identifier, "?")
		fileName, err := canonicalizeDAGFile(topicType, fileName)
		if err != nil || !hasQuery {
			return fileName, err
		}
		if query != dagHistoryWithBackfill {
			return "", fmt.Errorf("topic %q takes only %s", topicType, dagHistoryWithBackfill)
		}
		return fileName + "?" + query, nil
	case TopicTypeDAGRun, TopicTypeSubDAGRun, TopicTypeStepLog, TopicTypeQueueItems, TopicTypeBackfill:
		if identifier == "" {
			return "", fmt.Errorf("topic %q requires an identifier", topicType)
		}
		return identifier, nil
	default:
		// Keep unknown topic types parseable for forward compatibility,
		// but still require a non-empty identifier to avoid ambiguous routing.
		if identifier == "" {
			return "", fmt.Errorf("topic %q requires an identifier", topicType)
		}
		return identifier, nil
	}
}

func canonicalizeDAGFile(topicType TopicType, fileName string) (string, error) {
	if fileName == "" {
		return "", fmt.Errorf("topic %q requires an identifier", topicType)
	}
	if err := ir.ValidateDAGName(fileName); err != nil {
		return "", fmt.Errorf("invalid DAG file name: %w", err)
	}
	return fileName, nil
}
