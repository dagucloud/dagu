// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	trustedProxyUserMaxBytes      = 255
	trustedProxyGroupMaxBytes     = 512
	trustedProxyGroupsMaxRawBytes = 64 * 1024
	trustedProxyGroupsMaxCount    = 1000
)

var (
	errTrustedProxyIdentityUnavailable = errors.New("trusted proxy identity unavailable")
	errTrustedProxyIdentityInvalid     = errors.New("trusted proxy identity invalid")
)

type trustedProxyIdentity struct {
	user   string
	groups []string
}

func parseTrustedProxyIdentity(headers http.Header, userHeader, groupsHeader string, groupsRequired bool) (trustedProxyIdentity, error) {
	user, err := parseTrustedProxyUser(headers.Values(userHeader))
	if err != nil {
		return trustedProxyIdentity{}, err
	}

	groupValues := headers.Values(groupsHeader)
	if len(groupValues) == 0 {
		if groupsRequired {
			return trustedProxyIdentity{}, errTrustedProxyIdentityUnavailable
		}
		return trustedProxyIdentity{user: user}, nil
	}

	groups, err := parseTrustedProxyGroups(groupValues)
	if err != nil {
		return trustedProxyIdentity{}, err
	}
	return trustedProxyIdentity{user: user, groups: groups}, nil
}

func parseTrustedProxyUser(values []string) (string, error) {
	if len(values) == 0 {
		return "", errTrustedProxyIdentityUnavailable
	}
	if len(values) != 1 {
		return "", errTrustedProxyIdentityInvalid
	}

	value := trimHTTPOptionalWhitespace(values[0])
	if value == "" {
		return "", errTrustedProxyIdentityUnavailable
	}
	if len(value) > trustedProxyUserMaxBytes || !validTrustedProxyText(value) {
		return "", errTrustedProxyIdentityInvalid
	}
	return value, nil
}

func parseTrustedProxyGroups(values []string) ([]string, error) {
	rawBytes := 0
	for _, value := range values {
		rawBytes += len(value)
		if rawBytes > trustedProxyGroupsMaxRawBytes {
			return nil, errTrustedProxyIdentityInvalid
		}
	}

	seen := make(map[string]struct{})
	groups := make([]string, 0)
	nonemptyCount := 0
	for _, value := range values {
		reader := csv.NewReader(strings.NewReader(value))
		reader.FieldsPerRecord = -1
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			continue
		}
		if err != nil {
			return nil, errTrustedProxyIdentityInvalid
		}
		if _, err := reader.Read(); !errors.Is(err, io.EOF) {
			return nil, errTrustedProxyIdentityInvalid
		}

		for _, field := range record {
			group := trimHTTPOptionalWhitespace(field)
			if group == "" {
				continue
			}
			nonemptyCount++
			if nonemptyCount > trustedProxyGroupsMaxCount || len(group) > trustedProxyGroupMaxBytes || !validTrustedProxyText(group) {
				return nil, errTrustedProxyIdentityInvalid
			}
			if _, ok := seen[group]; ok {
				continue
			}
			seen[group] = struct{}{}
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func trimHTTPOptionalWhitespace(value string) string {
	return strings.Trim(value, " \t")
}

func validTrustedProxyText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
