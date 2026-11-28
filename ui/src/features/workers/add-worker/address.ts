// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { HostEnvironment, type components } from '@/api/v1/schema';

type JoinReadiness = components['schemas']['WorkerJoinReadinessResponse'];

export const DEFAULT_COORDINATOR_PORT = 50055;

/** The address a worker dials, as the host and port fields hold it. */
export interface WorkerAddress {
  host: string;
  port: string;
}

/** A problem with the address fields: a static message and its values. */
export interface AddressProblem {
  text: string;
  values?: Record<string, string>;
}

const DNS_NAME =
  /^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$/;
const IPV6 = /^[0-9A-Fa-f:.]+$/;

/** Splits "host:port" or "[v6]:port". A value without a port keeps it empty. */
export function splitAddress(address: string): WorkerAddress {
  const bracketed = /^\[([^\]]+)\](?::(\d*))?$/.exec(address);
  if (bracketed) {
    return { host: bracketed[1] ?? '', port: bracketed[2] ?? '' };
  }
  const colon = address.lastIndexOf(':');
  if (colon < 0 || address.indexOf(':') !== colon) {
    return { host: address, port: '' };
  }
  return { host: address.slice(0, colon), port: address.slice(colon + 1) };
}

/** Joins a host and a port, bracketing an IPv6 address. */
export function joinAddress({ host, port }: WorkerAddress): string {
  const h = host.trim().replace(/^\[(.*)\]$/, '$1');
  return `${h.includes(':') ? `[${h}]` : h}:${port.trim()}`;
}

/**
 * The address step 2 starts with: the last invite's address, else an
 * explicit coordinator.advertise, else an empty host. The port defaults to
 * the coordinator's. Nothing here comes from the browser's location.
 */
export function initialAddress(readiness: JoinReadiness): WorkerAddress {
  const reports = readiness.coordinators.flatMap((c) =>
    c.report ? [c.report] : []
  );
  const port = String(reports[0]?.port ?? DEFAULT_COORDINATOR_PORT);
  if (readiness.lastInviteAddress) {
    const last = splitAddress(readiness.lastInviteAddress);
    return { host: last.host, port: last.port || port };
  }
  const advertise = reports.find((r) => r.advertise)?.advertise;
  return { host: advertise ? splitAddress(advertise).host : '', port };
}

/**
 * The hosts step 2 suggests: the interface addresses each coordinator on a
 * host reports. A container or a pod reports none, since its own addresses
 * are not ones a worker elsewhere can dial.
 */
export function suggestedHosts(readiness: JoinReadiness): string[] {
  const hosts = readiness.coordinators.flatMap((c) =>
    c.report && (c.report.environment ?? HostEnvironment.host) === 'host'
      ? c.report.addresses
      : []
  );
  return [...new Set(hosts)];
}

/** What is wrong with the address fields, or undefined when they are usable. */
export function addressProblem(
  address: WorkerAddress
): AddressProblem | undefined {
  const raw = address.host.trim();
  if (raw === '') {
    return { text: 'Enter the address workers use to reach the coordinator.' };
  }
  if (/[\s/]/.test(raw)) {
    return {
      text: 'Enter a host name or IP address only, without a scheme or path.',
    };
  }
  const host = raw.replace(/^\[(.*)\]$/, '$1');
  if (host.includes(':') && host === raw && host.split(':').length === 2) {
    return { text: 'Enter the port in the Port field.' };
  }
  if (host.includes(':') ? !IPV6.test(host) : !DNS_NAME.test(host)) {
    return {
      text: '{host} is not a host name or IP address.',
      values: { host: raw },
    };
  }
  const lower = host.toLowerCase();
  if (lower === 'localhost' || /^127\./.test(lower) || lower === '::1') {
    return {
      text: '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.',
      values: { host: raw },
    };
  }
  if (lower === '0.0.0.0' || lower === '::') {
    return {
      text: '{host} is a listen address. Enter an address that workers can dial.',
      values: { host: raw },
    };
  }
  const port = address.port.trim();
  if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
    return { text: 'Enter a port from 1 to 65535.' };
  }
  return undefined;
}
