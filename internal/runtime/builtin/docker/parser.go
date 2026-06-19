// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

// volumeSpec represents a parsed volume specification
type volumeSpec struct {
	Source string
	Target string
	Mode   string // "ro", "rw", or empty (defaults to rw)
}

// parseVolumes parses volume specifications into bind mounts and volume mounts.
func parseVolumes(workDir string, volumes []string, fieldPrefix string) ([]string, []mount.Mount, error) {
	var binds []string
	var mounts []mount.Mount

	for i, vol := range volumes {
		fieldPath := fmt.Sprintf("%s[%d]", fieldPrefix, i)
		spec, err := parseVolumeSpec(vol)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", fieldPath, err)
		}

		target := spec.Target
		readOnly := false

		if spec.Mode == "ro" {
			readOnly = true
		} else if spec.Mode != "" && spec.Mode != "rw" {
			return nil, nil, fmt.Errorf("%s: %w: invalid mode %s in %s", fieldPath, ErrInvalidVolumeFormat, spec.Mode, vol)
		}

		source, err := resolveVolumeSource(workDir, spec.Source, fieldPath)
		if err != nil {
			return nil, nil, err
		}

		if source.isBind {
			bindStr := source.value + ":" + target
			if readOnly {
				bindStr += ":ro"
			} else {
				bindStr += ":rw"
			}
			binds = append(binds, bindStr)
		} else {
			mnt := mount.Mount{
				Type:     mount.TypeVolume,
				Source:   source.value,
				Target:   target,
				ReadOnly: readOnly,
			}
			mounts = append(mounts, mnt)
		}
	}

	return binds, mounts, nil
}

// parsePorts parses port specifications into ExposedPorts and PortBindings
func parsePorts(ports []string) (network.PortSet, network.PortMap, error) {
	exposedPorts := make(network.PortSet)
	portBindings := make(network.PortMap)

	for _, portSpec := range ports {
		// Remove any whitespace
		portSpec = strings.TrimSpace(portSpec)

		// Split by colon to get components
		parts := strings.Split(portSpec, ":")

		var hostIP, hostPort, containerPort, proto string

		switch len(parts) {
		case 1:
			// Format: "80" or "80/tcp"
			containerPort = parts[0]
		case 2:
			// Format: "8080:80"
			hostPort = parts[0]
			containerPort = parts[1]
		case 3:
			// Format: "0.0.0.0:8080:80"
			hostIP = parts[0]
			hostPort = parts[1]
			containerPort = parts[2]
		default:
			return nil, nil, fmt.Errorf("%w: %s", ErrInvalidPortFormat, portSpec)
		}

		// Extract protocol if specified
		if strings.Contains(containerPort, "/") {
			protoParts := strings.Split(containerPort, "/")
			if len(protoParts) != 2 {
				return nil, nil, fmt.Errorf("%w: invalid protocol in %s", ErrInvalidPortFormat, portSpec)
			}
			containerPort = protoParts[0]
			proto = protoParts[1]
		} else {
			proto = "tcp" // Default to TCP
		}

		// Validate protocol
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return nil, nil, fmt.Errorf("%w: invalid protocol %s in %s", ErrInvalidPortFormat, proto, portSpec)
		}

		parsedPort, err := network.ParsePort(containerPort + "/" + proto)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid container port %s in %s", ErrInvalidPortFormat, containerPort, portSpec)
		}

		// Add to exposed ports
		exposedPorts[parsedPort] = struct{}{}

		// Add to port bindings if host port is specified
		if hostPort != "" {
			if hostIP == "" {
				hostIP = "0.0.0.0" // Default to all interfaces
			}

			addr, err := netip.ParseAddr(hostIP)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: invalid host IP %s in %s", ErrInvalidPortFormat, hostIP, portSpec)
			}

			portBindings[parsedPort] = []network.PortBinding{
				{
					HostIP:   addr,
					HostPort: hostPort,
				},
			}
		}
	}

	return exposedPorts, portBindings, nil
}

// parseNetworkMode converts a network string to container.NetworkMode
func parseNetworkMode(network string) container.NetworkMode {
	// Standard network modes
	switch network {
	case "bridge", "host", "none":
		return container.NetworkMode(network)
	default:
		// Check if it's a container network reference
		if strings.HasPrefix(network, "container:") {
			return container.NetworkMode(network)
		}
		// Otherwise, it's a custom network name
		return container.NetworkMode(network)
	}
}

// isStandardNetworkMode checks if the network mode is a standard Docker network mode
func isStandardNetworkMode(network string) bool {
	return network == "bridge" || network == "host" || network == "none" ||
		strings.HasPrefix(network, "container:") || network == ""
}
