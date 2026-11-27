// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// kubernetesServiceHostEnv is set in every Kubernetes pod.
	kubernetesServiceHostEnv = "KUBERNETES_SERVICE_HOST"
	// joinReadinessTimeout bounds the call to each coordinator, so a
	// registry entry that no longer answers does not hold the reply.
	joinReadinessTimeout = 10 * time.Second
)

// containerMarkerFiles are created inside containers by Docker and Podman.
var containerMarkerFiles = []string{"/.dockerenv", "/run/.containerenv"}

var configSourceKinds = map[appconfig.SourceKind]coordinatorv1.ConfigSourceKind{
	appconfig.SourceKindDefault: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT,
	appconfig.SourceKindFile:    coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE,
	appconfig.SourceKindEnv:     coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV,
	appconfig.SourceKindFlag:    coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG,
}

// JoinReadinessClient reads the join readiness of the coordinators.
type JoinReadinessClient interface {
	// GetJoinReadiness asks every coordinator in the registry at once and
	// returns one entry per coordinator. A coordinator that does not answer
	// within 10 seconds gets an error entry. It fails only when the registry
	// cannot be read or lists no coordinator.
	GetJoinReadiness(ctx context.Context) ([]JoinReadiness, error)
}

// JoinReadiness is the join readiness of one coordinator.
type JoinReadiness struct {
	// Coordinator is the registry entry of the coordinator.
	Coordinator serviceregistry.HostInfo
	// Report is what the coordinator reported. It is nil when Err is set.
	Report *coordinatorv1.GetJoinReadinessResponse
	// Err is why the coordinator did not report, with the gRPC status of a
	// refused call, such as Unauthenticated without the peer token.
	Err error
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
	// BuiltinTLSReason is why BuiltinTLS is false.
	BuiltinTLSReason coordinatorv1.BuiltinTLSReason
}

var _ JoinReadinessClient = (*clientImpl)(nil)

// hostProbe reads where the coordinator process runs.
type hostProbe struct {
	environment    func() coordinatorv1.HostEnvironment
	interfaceAddrs func() ([]net.Addr, error)
}

// processEnvironment is the environment of this process. It cannot change
// while the process runs, so the marker files are checked once.
var processEnvironment = sync.OnceValue(func() coordinatorv1.HostEnvironment {
	return detectEnvironment(os.Getenv, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
})

func systemHostProbe() hostProbe {
	return hostProbe{environment: processEnvironment, interfaceAddrs: net.InterfaceAddrs}
}

// detectEnvironment applies the rule of Spec 108 §Environment.
func detectEnvironment(getenv func(string) string, exists func(string) bool) coordinatorv1.HostEnvironment {
	// Kubernetes sets the variable whichever container runtime runs the pod.
	if getenv(kubernetesServiceHostEnv) != "" {
		return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
	}
	if slices.ContainsFunc(containerMarkerFiles, exists) {
		return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER
	}
	return coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST
}

// interfaceAddresses returns the interface addresses that a worker on another
// host may dial: neither loopback nor link-local. A failure to list them
// yields none, since they are only suggestions.
func (p hostProbe) interfaceAddresses(ctx context.Context) []string {
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
		addresses = h.listenAddresses(ctx, cfg.ListenHost)
	}
	reason := cfg.BuiltinTLSReason
	if cfg.BuiltinTLS {
		reason = coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_UNSPECIFIED
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
		Advertise:        cfg.Advertise,
		Environment:      environment,
		Addresses:        addresses,
		BuiltinTls:       cfg.BuiltinTLS,
		BuiltinTlsReason: reason,
		Ready:            !loopbackOnly && cfg.BuiltinTLS,
	}, nil
}

// listenAddresses returns the addresses on which a listener on host accepts
// workers from other hosts: the interface addresses for a listener on all
// interfaces, host itself for another IP address that is not loopback, and
// none for a host name.
func (h *Handler) listenAddresses(ctx context.Context, host string) []string {
	ip := listenIP(host)
	switch {
	case host == "" || ip.IsUnspecified():
		return h.hostProbe.interfaceAddresses(ctx)
	case ip != nil && !ip.IsLoopback():
		return []string{ip.String()}
	default:
		return nil
	}
}

// isLoopbackHost reports whether a listener on host accepts connections from
// this machine only: host is localhost, an address in 127.0.0.0/8, or [::1].
func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || listenIP(host).IsLoopback()
}

// listenIP returns the IP address that host names, or nil for a host name.
// An IPv6 address may be in brackets, the form a coordinator listens on.
func listenIP(host string) net.IP {
	return net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
}

func (cli *clientImpl) GetJoinReadiness(ctx context.Context) ([]JoinReadiness, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	reports := make([]JoinReadiness, len(members))
	var wg sync.WaitGroup
	for i, member := range members {
		wg.Go(func() {
			reports[i] = cli.joinReadiness(ctx, member)
		})
	}
	wg.Wait()
	return reports, nil
}

func (cli *clientImpl) joinReadiness(ctx context.Context, member serviceregistry.HostInfo) JoinReadiness {
	ctx, cancel := context.WithTimeout(ctx, joinReadinessTimeout)
	defer cancel()
	report := JoinReadiness{Coordinator: member}
	c, err := cli.getOrCreateDiscoveredClient(member)
	if err == nil {
		report.Report, err = c.client.GetJoinReadiness(ctx, &coordinatorv1.GetJoinReadinessRequest{})
	}
	if err != nil {
		report.Err = fmt.Errorf("get join readiness from %s: %w", coordinatorAddress(member), err)
	}
	return report
}
