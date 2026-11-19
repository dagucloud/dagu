// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Spec 092, "Worker pools". The key coordinator.worker_pools binds the labels
// of a worker to the identity of its client certificate. The block has a closed
// schema and no environment variable. An unknown key, a missing key or a wrong
// type stops Dagu at start. The loader changes no value to make it fit.

// WorkerPool is one entry of coordinator.worker_pools.
type WorkerPool struct {
	// Name identifies the pool in errors, in claims and in leases.
	Name string
	// Identities lists the subject Common Names of the client certificates of
	// the pool. An identity is in one pool only.
	Identities []string
	// Labels maps a label key to its allowed value. The value "*" allows any
	// value.
	Labels map[string]string
}

// WorkerPoolAnyValue is the label value that allows any value.
const WorkerPoolAnyValue = "*"

const workerPoolsKey = "coordinator.worker_pools"

var workerPoolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// workerPoolKeys lists the keys of one pool, with the expected type for the
// error messages.
var workerPoolKeys = map[string]string{
	"name":       "a string that matches " + workerPoolNamePattern.String(),
	"identities": "a non-empty list of certificate identities",
	"labels":     "an object that maps a label key to its allowed value",
}

// ParseWorkerPools checks the raw value of coordinator.worker_pools and returns
// the typed pools. raw is nil when the file has no such key.
func ParseWorkerPools(raw any) ([]WorkerPool, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, poolConfigError(workerPoolsKey, "a non-empty list of worker pools", raw)
	}
	pools := make([]WorkerPool, 0, len(list))
	names := map[string]struct{}{}
	owners := map[string]string{}
	for i, item := range list {
		field := fmt.Sprintf("%s[%d]", workerPoolsKey, i)
		pool, err := parseWorkerPool(field, item)
		if err != nil {
			return nil, err
		}
		if _, dup := names[pool.Name]; dup {
			return nil, poolConfigError(field+".name", "a name that no other pool has", pool.Name)
		}
		names[pool.Name] = struct{}{}
		for j, identity := range pool.Identities {
			if other, dup := owners[identity]; dup {
				return nil, fmt.Errorf("config: key %q: identity %q is in the pools %q and %q: an identity is in one pool only",
					fmt.Sprintf("%s.identities[%d]", field, j), identity, other, pool.Name)
			}
			owners[identity] = pool.Name
		}
		pools = append(pools, pool)
	}
	return pools, nil
}

func parseWorkerPool(field string, raw any) (WorkerPool, error) {
	var pool WorkerPool
	block, ok := raw.(map[string]any)
	if !ok {
		return pool, poolConfigError(field, "an object with the keys name, identities and labels", raw)
	}
	for _, key := range sortedMapKeys(block) {
		if _, valid := workerPoolKeys[key]; !valid {
			return pool, fmt.Errorf("config: unknown key %q in the block %s; valid keys are %s",
				field+"."+key, workerPoolsKey, strings.Join(sortedMapKeys(workerPoolKeys), ", "))
		}
	}

	name, isString := block["name"].(string)
	if !isString || !workerPoolNamePattern.MatchString(name) {
		return pool, poolConfigError(field+".name", workerPoolKeys["name"], block["name"])
	}
	pool.Name = name

	identities, isList := block["identities"].([]any)
	if !isList || len(identities) == 0 {
		return pool, poolConfigError(field+".identities", workerPoolKeys["identities"], block["identities"])
	}
	for j, item := range identities {
		identity, isString := item.(string)
		if !isString || strings.TrimSpace(identity) == "" || strings.TrimSpace(identity) != identity {
			return pool, poolConfigError(fmt.Sprintf("%s.identities[%d]", field, j),
				"a non-empty string without leading or trailing spaces", item)
		}
		if slices.Contains(pool.Identities, identity) {
			return pool, poolConfigError(fmt.Sprintf("%s.identities[%d]", field, j), "an identity that the list names once", identity)
		}
		pool.Identities = append(pool.Identities, identity)
	}

	if value, present := block["labels"]; present {
		labels, err := parseWorkerPoolLabels(field+".labels", value)
		if err != nil {
			return pool, err
		}
		pool.Labels = labels
	}
	return pool, nil
}

func parseWorkerPoolLabels(field string, raw any) (map[string]string, error) {
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, poolConfigError(field, workerPoolKeys["labels"], raw)
	}
	labels := make(map[string]string, len(block))
	providers := 0
	for _, key := range sortedMapKeys(block) {
		keyField := field + "." + key
		value, isString := block[key].(string)
		if !isString || value == "" {
			return nil, poolConfigError(keyField, `a non-empty string, such as "true" or "*"`, block[key])
		}
		switch {
		case key == "os" || key == "arch":
			return nil, fmt.Errorf("config: key %q: every pool allows the label %q with any value, because Dagu sets it; remove the key",
				keyField, key)
		case strings.HasPrefix(key, "agent.provider."):
			if strings.TrimPrefix(key, "agent.provider.") == "" {
				return nil, fmt.Errorf("config: key %q: expected agent.provider.<provider>, got %q", keyField, key)
			}
			if value != "true" {
				return nil, poolConfigError(keyField, `the value "true"`, value)
			}
			providers++
			if providers > 1 {
				return nil, fmt.Errorf("config: key %q: a pool allows one label agent.provider.<provider>, because an agent worker serves one provider",
					keyField)
			}
		case strings.HasPrefix(key, "agent."):
			return nil, fmt.Errorf("config: key %q: the labels that start with agent. belong to Dagu; a pool can allow only agent.provider.<provider>",
				keyField)
		}
		labels[key] = value
	}
	return labels, nil
}

func poolConfigError(key, expected string, got any) error {
	return fmt.Errorf("config: key %q: expected %s, got %s", key, expected, showConfigValue(got))
}

// loadWorkerPoolsConfig reads coordinator.worker_pools. The key has no default
// and no environment variable.
func (l *ConfigLoader) loadWorkerPoolsConfig(cfg *Config) error {
	pools, err := ParseWorkerPools(l.v.Get(workerPoolsKey))
	if err != nil {
		return err
	}
	dispatchers, err := ParseDispatchers(l.v.Get(dispatchersKey), pools)
	if err != nil {
		return err
	}
	cfg.Coordinator.WorkerPools = pools
	cfg.Coordinator.Dispatchers = dispatchers
	return nil
}

const dispatchersKey = "coordinator.dispatchers"

// ParseDispatchers checks the raw value of coordinator.dispatchers against the
// pools. The key is required with pools and invalid without them. An identity
// cannot be a dispatcher and a member of a pool.
func ParseDispatchers(raw any, pools []WorkerPool) ([]string, error) {
	if raw == nil {
		if len(pools) > 0 {
			return nil, fmt.Errorf("config: key %q is not set: %s needs the certificate identities of the server, the scheduler and the other dispatchers",
				dispatchersKey, workerPoolsKey)
		}
		return nil, nil
	}
	if len(pools) == 0 {
		return nil, fmt.Errorf("config: key %q needs %s: without pools, each valid certificate can dispatch", dispatchersKey, workerPoolsKey)
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, poolConfigError(dispatchersKey, "a non-empty list of certificate identities", raw)
	}
	members := map[string]string{}
	for _, pool := range pools {
		for _, identity := range pool.Identities {
			members[identity] = pool.Name
		}
	}
	dispatchers := make([]string, 0, len(list))
	for i, item := range list {
		field := fmt.Sprintf("%s[%d]", dispatchersKey, i)
		identity, isString := item.(string)
		if !isString || strings.TrimSpace(identity) == "" || strings.TrimSpace(identity) != identity {
			return nil, poolConfigError(field, "a non-empty string without leading or trailing spaces", item)
		}
		if slices.Contains(dispatchers, identity) {
			return nil, poolConfigError(field, "an identity that the list names once", identity)
		}
		if pool, member := members[identity]; member {
			return nil, fmt.Errorf("config: key %q: identity %q is a dispatcher and a member of the pool %q: an identity has one role",
				field, identity, pool)
		}
		dispatchers = append(dispatchers, identity)
	}
	return dispatchers, nil
}
