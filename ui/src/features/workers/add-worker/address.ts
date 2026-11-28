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

/** A problem with the address fields: the field, a message and its values. */
export interface AddressProblem {
  field: 'host' | 'port';
  text: string;
  values?: Record<string, string>;
}

// The rules of Spec 108 §Add Worker, which the MCP action applies too: one
// IP address without a zone, or one DNS name.
const DNS_NAME =
  /^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.?$/;
const IPV4_OCTET = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]\\d|\\d)';
const IPV4 = new RegExp(`^${IPV4_OCTET}(\\.${IPV4_OCTET}){3}$`);
// A short or full container ID, as a container's host name.
const CONTAINER_ID = /^[0-9a-f]{12}([0-9a-f]{52})?$/;
// "host:port" typed into the host field, bracketed or not.
const HOST_WITH_PORT = /^(?:\[([^\]]+)\]|([^:[\]]+)):(\d*)$/;

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
 * explicit coordinator.advertise with that coordinator's port, else an
 * empty host and the first coordinator's port. Nothing here comes from the
 * browser's location.
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
  const advertised = reports.find((r) => r.advertise);
  if (advertised?.advertise) {
    return {
      host: splitAddress(advertised.advertise).host,
      port: String(advertised.port),
    };
  }
  return { host: '', port };
}

/** Where the first coordinator that reported runs. */
export function coordinatorEnvironment(
  readiness: JoinReadiness
): HostEnvironment | undefined {
  return readiness.coordinators.find((c) => c.report)?.report?.environment;
}

/**
 * The addresses step 2 suggests: each interface address a coordinator on a
 * host reports, with that coordinator's port. A container or a pod reports
 * none, since its own addresses are not ones a worker elsewhere can dial.
 */
export function suggestedAddresses(readiness: JoinReadiness): WorkerAddress[] {
  const seen = new Set<string>();
  const out: WorkerAddress[] = [];
  for (const { report } of readiness.coordinators) {
    if (!report || (report.environment ?? HostEnvironment.host) !== 'host') {
      continue;
    }
    for (const host of report.addresses) {
      const address = { host, port: String(report.port) };
      const key = joinAddress(address);
      if (!seen.has(key)) {
        seen.add(key);
        out.push(address);
      }
    }
  }
  return out;
}

const LISTEN =
  '{host} is a listen address. Enter an address that workers can dial.';
const LOOPBACK =
  '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.';
const LINK_LOCAL =
  '{host} is a link-local address, which another network does not route. Enter another address of the machine.';

type ParsedIP = { v4: number[] } | { v6: number[] };

// Parses an IP address as Go's net.ParseIP does, without a zone. An IPv6
// address that maps an IPv4 one parses as that IPv4 address.
function parseIP(host: string): ParsedIP | undefined {
  if (IPV4.test(host)) return { v4: host.split('.').map(Number) };
  if (!host.includes(':') || host.includes('%')) return undefined;
  let canonical: string;
  try {
    canonical = new URL(`http://[${host}]`).hostname.slice(1, -1);
  } catch {
    return undefined;
  }
  const [head = '', tail] = canonical.split('::');
  const left = head ? head.split(':') : [];
  const right = tail ? tail.split(':') : [];
  const hextets = [
    ...left,
    ...Array<string>(8 - left.length - right.length).fill('0'),
    ...right,
  ].map((h) => parseInt(h, 16));
  if (hextets.slice(0, 5).every((h) => h === 0) && hextets[5] === 0xffff) {
    const [hi = 0, lo = 0] = hextets.slice(6);
    return { v4: [hi >> 8, hi & 0xff, lo >> 8, lo & 0xff] };
  }
  return { v6: hextets };
}

function ipProblem(ip: ParsedIP): string | undefined {
  if ('v4' in ip) {
    const [a, b] = ip.v4;
    if (ip.v4.every((n) => n === 0)) return LISTEN;
    if (a === 127) return LOOPBACK;
    if (a === 169 && b === 254) return LINK_LOCAL;
    return undefined;
  }
  const h = ip.v6;
  if (h.every((n) => n === 0)) return LISTEN;
  if (h.slice(0, 7).every((n) => n === 0) && h[7] === 1) return LOOPBACK;
  if (((h[0] ?? 0) & 0xffc0) === 0xfe80) return LINK_LOCAL;
  return undefined;
}

function hostProblem(host: string): string | undefined {
  const ip = parseIP(host);
  if (ip) return ipProblem(ip);
  if (!DNS_NAME.test(host)) {
    return '{host} is not one host. Enter an IP address without a zone, or a DNS name.';
  }
  if (host.replace(/\.$/, '').toLowerCase() === 'localhost') return LOOPBACK;
  if (CONTAINER_ID.test(host)) {
    return '{host} looks like a container ID. Enter the address of the machine that runs the container.';
  }
  return undefined;
}

/**
 * What is wrong with the address fields, or undefined when a worker on
 * another machine can dial them.
 */
export function addressProblem(
  address: WorkerAddress
): AddressProblem | undefined {
  const raw = address.host.trim();
  if (raw === '') {
    return {
      field: 'host',
      text: 'Enter the address workers use to reach the coordinator.',
    };
  }
  const withPort = HOST_WITH_PORT.exec(raw);
  const host = withPort
    ? (withPort[1] ?? withPort[2] ?? '')
    : raw.replace(/^\[(.*)\]$/, '$1');
  const problem = hostProblem(host);
  if (problem) return { field: 'host', text: problem, values: { host } };
  if (withPort) {
    return { field: 'host', text: 'Enter the port in the Port field.' };
  }
  const port = address.port.trim();
  if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
    return { field: 'port', text: 'Enter a port from 1 to 65535.' };
  }
  return undefined;
}
