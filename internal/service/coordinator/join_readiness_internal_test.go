// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hostEnvironment       = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST
	containerEnvironment  = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER
	kubernetesEnvironment = coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
)

// fakeHostProbe returns a probe that reports environment and interface
// addresses parsed from cidrs.
func fakeHostProbe(t *testing.T, environment coordinatorv1.HostEnvironment, cidrs ...string) hostProbe {
	t.Helper()
	addrs := make([]net.Addr, 0, len(cidrs))
	for _, cidr := range cidrs {
		ip, ipNet, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		ipNet.IP = ip
		addrs = append(addrs, ipNet)
	}
	return hostProbe{
		environment:    func() coordinatorv1.HostEnvironment { return environment },
		interfaceAddrs: func() ([]net.Addr, error) { return addrs, nil },
	}
}

func joinReadiness(t *testing.T, cfg JoinReadinessConfig, probe hostProbe) *coordinatorv1.GetJoinReadinessResponse {
	t.Helper()
	h := NewHandler(HandlerConfig{JoinReadiness: cfg})
	h.hostProbe = probe
	resp, err := h.GetJoinReadiness(context.Background(), &coordinatorv1.GetJoinReadinessRequest{})
	require.NoError(t, err)
	return resp
}

// Each host is a coordinator.host value a coordinator starts with. Only a
// listener on all interfaces lists the interface addresses.
func TestJoinReadinessListenHost(t *testing.T) {
	interfaces := []string{"127.0.0.1/8", "192.168.1.20/24", "10.0.0.5/8"}
	allInterfaces := []string{"192.168.1.20", "10.0.0.5"}
	tests := []struct {
		host          string
		loopbackOnly  bool
		wantAddresses []string
	}{
		{host: "127.0.0.1", loopbackOnly: true},
		{host: "127.0.1.1", loopbackOnly: true},
		{host: "localhost", loopbackOnly: true},
		{host: "[::1]", loopbackOnly: true},
		{host: "", wantAddresses: allInterfaces},
		{host: "0.0.0.0", wantAddresses: allInterfaces},
		{host: "[::]", wantAddresses: allInterfaces},
		{host: "192.168.1.20", wantAddresses: []string{"192.168.1.20"}},
		{host: "[2001:db8::5]", wantAddresses: []string{"2001:db8::5"}},
		{host: "coordinator.example.com"},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			for _, builtinTLS := range []bool{false, true} {
				resp := joinReadiness(t,
					JoinReadinessConfig{ListenHost: test.host, Port: 50055, BuiltinTLS: builtinTLS},
					fakeHostProbe(t, hostEnvironment, interfaces...))
				assert.Equal(t, test.host, resp.ListenHost)
				assert.Equal(t, int32(50055), resp.Port)
				assert.Equal(t, test.loopbackOnly, resp.LoopbackOnly)
				assert.Equal(t, test.wantAddresses, resp.Addresses)
				assert.Equal(t, builtinTLS, resp.BuiltinTls)
				assert.Equal(t, !test.loopbackOnly && builtinTLS, resp.Ready)
			}
		})
	}
}

func TestJoinReadinessBuiltinTLSReason(t *testing.T) {
	ownCertificates := coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES
	resp := joinReadiness(t, JoinReadinessConfig{BuiltinTLSReason: ownCertificates}, fakeHostProbe(t, hostEnvironment))
	assert.False(t, resp.BuiltinTls)
	assert.Equal(t, ownCertificates, resp.BuiltinTlsReason)

	resp = joinReadiness(t, JoinReadinessConfig{BuiltinTLS: true, BuiltinTLSReason: ownCertificates}, fakeHostProbe(t, hostEnvironment))
	assert.True(t, resp.BuiltinTls)
	assert.Equal(t, coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_UNSPECIFIED, resp.BuiltinTlsReason)
}

func TestJoinReadinessReportsSettings(t *testing.T) {
	tests := []struct {
		name   string
		source appconfig.ValueSource
		want   *coordinatorv1.ConfigSource
	}{
		{
			name:   "default",
			source: appconfig.ValueSource{Kind: appconfig.SourceKindDefault},
			want:   &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT},
		},
		{
			name:   "file",
			source: appconfig.ValueSource{Kind: appconfig.SourceKindFile, Name: "/etc/dagu/config.yaml"},
			want:   &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE, Name: "/etc/dagu/config.yaml"},
		},
		{
			name:   "env",
			source: appconfig.ValueSource{Kind: appconfig.SourceKindEnv, Name: "DAGU_COORDINATOR_HOST"},
			want:   &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV, Name: "DAGU_COORDINATOR_HOST"},
		},
		{
			name:   "flag",
			source: appconfig.ValueSource{Kind: appconfig.SourceKindFlag, Name: "--coordinator.host"},
			want:   &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG, Name: "--coordinator.host"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := joinReadiness(t, JoinReadinessConfig{
				ListenHost: "127.0.0.1",
				ConfigFile: "/home/dagu/.config/dagu/config.yaml",
				HostSource: test.source,
				Advertise:  "dagu.example.com",
			}, fakeHostProbe(t, hostEnvironment))
			assert.Equal(t, test.want.Kind, resp.HostSource.Kind)
			assert.Equal(t, test.want.Name, resp.HostSource.Name)
			assert.Equal(t, "/home/dagu/.config/dagu/config.yaml", resp.ConfigFile)
			assert.Equal(t, "dagu.example.com", resp.Advertise)
		})
	}
}

func TestDetectEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		files []string
		want  coordinatorv1.HostEnvironment
	}{
		{name: "host", want: hostEnvironment},
		{name: "docker", files: []string{"/.dockerenv"}, want: containerEnvironment},
		{name: "podman", files: []string{"/run/.containerenv"}, want: containerEnvironment},
		{
			name:  "kubernetes",
			env:   map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"},
			files: []string{"/.dockerenv"},
			want:  kubernetesEnvironment,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := detectEnvironment(
				func(key string) string { return test.env[key] },
				func(path string) bool { return slices.Contains(test.files, path) },
			)
			assert.Equal(t, test.want, got)
		})
	}
}

// Addresses are reported only on a host, where they are the machine's own.
func TestJoinReadinessEnvironment(t *testing.T) {
	for _, environment := range []coordinatorv1.HostEnvironment{containerEnvironment, kubernetesEnvironment} {
		t.Run(environment.String(), func(t *testing.T) {
			for _, host := range []string{"0.0.0.0", "192.168.1.20"} {
				resp := joinReadiness(t, JoinReadinessConfig{ListenHost: host}, fakeHostProbe(t, environment, "192.168.1.20/24"))
				assert.Equal(t, environment, resp.Environment)
				assert.Empty(t, resp.Addresses)
			}
		})
	}
}

func TestJoinReadinessAddresses(t *testing.T) {
	probe := fakeHostProbe(t, hostEnvironment,
		"127.0.0.1/8",
		"::1/128",
		"169.254.10.1/16",
		"fe80::1/64",
		"10.0.0.5/8",
		"192.168.1.20/24",
		"2001:db8::5/64",
	)
	resp := joinReadiness(t, JoinReadinessConfig{ListenHost: "0.0.0.0"}, probe)
	assert.Equal(t, []string{"10.0.0.5", "192.168.1.20", "2001:db8::5"}, resp.Addresses)
}

// A failure to list interfaces leaves the suggestions empty and the rest of
// the report intact.
func TestJoinReadinessAddressesFail(t *testing.T) {
	probe := fakeHostProbe(t, hostEnvironment)
	probe.interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("no interfaces") }
	resp := joinReadiness(t, JoinReadinessConfig{ListenHost: "0.0.0.0", BuiltinTLS: true}, probe)
	assert.Empty(t, resp.Addresses)
	assert.True(t, resp.Ready)
}
