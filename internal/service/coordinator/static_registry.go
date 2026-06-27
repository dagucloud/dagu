// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// StaticRegistry is a simple service registry that returns a fixed list of coordinator addresses.
// This is useful for worker deployments where the coordinator addresses are known
// and specified via CLI flags or environment variables.
type StaticRegistry struct {
	coordinators []exec.HostInfo
}

// NewStaticRegistry creates a new StaticRegistry from a list of address strings.
// Each address should be in the format "host[:port]" or "[ipv6]:port".
func NewStaticRegistry(addresses []string) (*StaticRegistry, error) {
	hosts := make([]exec.HostInfo, 0, len(addresses))

	for _, addr := range addresses {
		if addr == "" {
			continue
		}

		host, port, err := parseAddress(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid coordinator address %q: %w", addr, err)
		}

		hosts = append(hosts, exec.HostInfo{
			ID:        fmt.Sprintf("coord-%d", len(hosts)),
			Host:      host,
			Port:      port,
			Status:    exec.ServiceStatusActive,
			StartedAt: time.Now(),
		})
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no valid coordinator addresses provided")
	}

	return &StaticRegistry{coordinators: hosts}, nil
}

// parseAddress parses a "host[:port]" or "[ipv6]:port" address string.
func parseAddress(addr string) (host string, port int, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, fmt.Errorf("host cannot be empty")
	}

	if host, portStr, err := net.SplitHostPort(addr); err == nil {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
		if p <= 0 || p > 65535 {
			return "", 0, fmt.Errorf("port must be between 1 and 65535")
		}
		if host == "" {
			return "", 0, fmt.Errorf("host cannot be empty")
		}
		return host, p, nil
	}

	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
		if !isIPv6Host(host) {
			return "", 0, fmt.Errorf("invalid address format, expected host[:port] or [ipv6]:port")
		}
		if host == "" {
			return "", 0, fmt.Errorf("host cannot be empty")
		}
		return host, 50055, nil
	}

	if !strings.Contains(addr, ":") || isIPv6Host(addr) {
		if addr == "" {
			return "", 0, fmt.Errorf("host cannot be empty")
		}
		return addr, 50055, nil
	}

	return "", 0, fmt.Errorf("invalid address format, expected host[:port] or [ipv6]:port")
}

func isIPv6Host(host string) bool {
	if strings.Count(host, ":") < 2 {
		return false
	}
	if i := strings.LastIndex(host, "%"); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host) != nil
}

// Register is a no-op for StaticRegistry since we don't support registration.
func (r *StaticRegistry) Register(_ context.Context, _ exec.ServiceName, _ exec.HostInfo) error {
	// Static registry doesn't support registration
	return nil
}

// Unregister is a no-op for StaticRegistry since we don't support registration.
func (r *StaticRegistry) Unregister(_ context.Context) {
	// Static registry doesn't support registration
}

// GetServiceMembers returns the list of coordinator hosts.
// Only ServiceNameCoordinator is supported; other services return an empty list.
func (r *StaticRegistry) GetServiceMembers(_ context.Context, name exec.ServiceName) ([]exec.HostInfo, error) {
	if name == exec.ServiceNameCoordinator {
		return r.coordinators, nil
	}
	// Return empty list for other services
	return nil, nil
}

// UpdateStatus is a no-op for StaticRegistry since we don't support status updates.
func (r *StaticRegistry) UpdateStatus(_ context.Context, _ exec.ServiceName, _ exec.ServiceStatus) error {
	// Static registry doesn't support status updates
	return nil
}

// Ensure StaticRegistry implements ServiceRegistry
var _ exec.ServiceRegistry = (*StaticRegistry)(nil)
