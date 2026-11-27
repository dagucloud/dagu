// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// kubernetesServiceHostEnv is set in every Kubernetes pod.
const kubernetesServiceHostEnv = "KUBERNETES_SERVICE_HOST"

// containerMarkerFiles are created inside containers by Docker and Podman.
var containerMarkerFiles = []string{"/.dockerenv", "/run/.containerenv"}

var configSourceKinds = map[appconfig.SourceKind]coordinatorv1.ConfigSourceKind{
	appconfig.SourceKindDefault: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT,
	appconfig.SourceKindFile:    coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE,
	appconfig.SourceKindEnv:     coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV,
	appconfig.SourceKindFlag:    coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG,
}

// JoinReadinessConfig holds the settings a coordinator reports as its join
// readiness.
type JoinReadinessConfig struct {
	// ListenHost and Port are coordinator.host and coordinator.port.
	ListenHost string
	Port       int
	// ConfigFile is the config file the coordinator read, or the default
	// path it would read.
	ConfigFile string
	// HostSource is where coordinator.host came from.
	HostSource appconfig.ValueSource
	// Advertise is coordinator.advertise when set explicitly, else empty.
	Advertise string
	// BuiltinTLS reports whether the coordinator has a usable peer CA.
	BuiltinTLS bool
}

// JoinReadinessClient reads the join readiness of the coordinators.
type JoinReadinessClient interface {
	// GetJoinReadiness returns one entry per coordinator in the registry. It
	// fails only when the registry cannot be read or lists no coordinator.
	GetJoinReadiness(ctx context.Context) ([]CoordinatorJoinReadiness, error)
}

// CoordinatorJoinReadiness is the join readiness of one coordinator.
type CoordinatorJoinReadiness struct {
	// Coordinator is the registry entry of the coordinator.
	Coordinator serviceregistry.HostInfo
	// Report is what the coordinator reported. It is nil when Err is set.
	Report *coordinatorv1.GetJoinReadinessResponse
	// Err is why the coordinator did not report, with the gRPC status of a
	// refused call, such as Unauthenticated without the peer token.
	Err error
}

var _ JoinReadinessClient = (*clientImpl)(nil)

// hostProbe reads where the coordinator process runs.
type hostProbe struct {
	getenv         func(string) string
	exists         func(string) bool
	interfaceAddrs func() ([]net.Addr, error)
}

func systemHostProbe() hostProbe {
	return hostProbe{
		getenv: os.Getenv,
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		interfaceAddrs: net.InterfaceAddrs,
	}
}

func (p hostProbe) environment() coordinatorv1.HostEnvironment {
	// Kubernetes sets the variable whichever container runtime runs the pod.
	if p.getenv(kubernetesServiceHostEnv) != "" {
		return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
	}
	for _, marker := range containerMarkerFiles {
		if p.exists(marker) {
			return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER
		}
	}
	return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST
}

// addresses returns the interface addresses that a worker on another host
// may dial: neither loopback nor link-local. A failure to list them yields
// none, since they are only suggestions.
func (p hostProbe) addresses(ctx context.Context) []string {
	addrs, err := p.interfaceAddrs()
	if err != nil {
		logger.Warn(ctx, "Failed to list interface addresses for join readiness", tag.Error(err))
		return nil
	}
	var result []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		result = append(result, ip.String())
	}
	return result
}

// GetJoinReadiness reports, from this coordinator's host, whether a worker
// on another host can join it.
func (h *Handler) GetJoinReadiness(ctx context.Context, _ *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	cfg := h.joinReadiness
	loopbackOnly := isLoopbackHost(cfg.ListenHost)
	environment := h.hostProbe.environment()
	var addresses []string
	if environment == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST {
		addresses = h.hostProbe.addresses(ctx)
	}
	return &coordinatorv1.GetJoinReadinessResponse{
		ListenHost:   cfg.ListenHost,
		Port:         int32(cfg.Port), //nolint:gosec // A TCP port fits in int32.
		ConfigFile:   cfg.ConfigFile,
		LoopbackOnly: loopbackOnly,
		HostSource: &coordinatorv1.ConfigSource{
			Kind: configSourceKinds[cfg.HostSource.Kind],
			Name: cfg.HostSource.Name,
		},
		Advertise:   cfg.Advertise,
		Environment: environment,
		Addresses:   addresses,
		BuiltinTls:  cfg.BuiltinTLS,
		Ready:       !loopbackOnly && cfg.BuiltinTLS,
	}, nil
}

// isLoopbackHost reports whether a listener on host accepts connections from
// this machine only: host is localhost, an address in 127.0.0.0/8, or ::1.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (cli *clientImpl) GetJoinReadiness(ctx context.Context) ([]CoordinatorJoinReadiness, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	reports := make([]CoordinatorJoinReadiness, 0, len(members))
	for _, member := range members {
		report := CoordinatorJoinReadiness{Coordinator: member}
		c, err := cli.getOrCreateDiscoveredClient(member)
		if err == nil {
			report.Report, err = c.client.GetJoinReadiness(ctx, &coordinatorv1.GetJoinReadinessRequest{})
		}
		if err != nil {
			report.Err = fmt.Errorf("get join readiness from %s: %w", coordinatorAddress(member), err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}
