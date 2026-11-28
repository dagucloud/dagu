// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { HostEnvironment, type components } from '@/api/v1/schema';

type JoinReadiness = components['schemas']['WorkerJoinReadinessResponse'];

/** A problem with the address: a static message and its values. */
export interface AddressProblem {
  text: string;
  values?: Record<string, string>;
}

// The rule of Spec 108 §Add Worker, which the MCP action applies with Go's
// net.SplitHostPort and net.ParseIP; the shared vectors in __fixtures__
// hold both to it. The host is one IP address without a zone, or one DNS
// name.
const DNS_NAME =
  /^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.?$/;
const IPV4_OCTET = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]\\d|\\d)';
const IPV4 = new RegExp(`^${IPV4_OCTET}(\\.${IPV4_OCTET}){3}$`);
// A short or full container ID, as a container's host name.
const CONTAINER_ID = /^[0-9a-f]{12}([0-9a-f]{52})?$/;
// An invite anywhere in a text: dagu1.<pin>.<secret>.
const INVITE = /dagu1\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}/;

// The characters Go's strings.TrimSpace removes.
const GO_SPACE_CLASS =
  '[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]';
const GO_SPACE = new RegExp(`^${GO_SPACE_CLASS}+|${GO_SPACE_CLASS}+$`, 'g');

/** Trims a text as Go's strings.TrimSpace does, as the MCP action does. */
export function goTrimSpace(text: string): string {
  return text.replace(GO_SPACE, '');
}

/** Whether a text holds an invite, which belongs only in the commands. */
export function holdsInvite(text: string): boolean {
  return INVITE.test(text);
}

/** Joins a host and a port as Go's net.JoinHostPort does. */
export function joinHostPort(host: string, port: number | string): string {
  return `${host.includes(':') ? `[${host}]` : host}:${port}`;
}

/**
 * The address step 2 starts with: the last invite's address, else an
 * explicit coordinator.advertise with that coordinator's port, else empty.
 * Only an address that passes addressProblem counts, and nothing here comes
 * from the browser's location.
 */
export function initialAddress(readiness: JoinReadiness): string {
  const candidates = [
    readiness.lastInviteAddress ?? '',
    ...readiness.coordinators.flatMap(({ report }) =>
      report?.advertise ? [joinHostPort(report.advertise, report.port)] : []
    ),
  ];
  return candidates.find((a) => a && !addressProblem(a)) ?? '';
}

/** Where the first coordinator that reported runs. */
export function coordinatorEnvironment(
  readiness: JoinReadiness
): HostEnvironment | undefined {
  return readiness.coordinators.find((c) => c.report)?.report?.environment;
}

/**
 * The address a pod advertises, with its coordinator's port, for workers
 * in the cluster: the first that passes addressProblem, else undefined.
 */
export function podAddress(readiness: JoinReadiness): string | undefined {
  return readiness.coordinators
    .flatMap(({ report }) =>
      report?.environment === HostEnvironment.kubernetes && report.advertise
        ? [joinHostPort(report.advertise, report.port)]
        : []
    )
    .find((a) => !addressProblem(a));
}

/** Whether any coordinator that reported runs in Kubernetes. */
export function inKubernetes(readiness: JoinReadiness): boolean {
  return readiness.coordinators.some(
    ({ report }) => report?.environment === HostEnvironment.kubernetes
  );
}

/**
 * The addresses step 2 suggests: each interface address a coordinator on a
 * host reports, with that coordinator's port. A container or a pod reports
 * none, since its own addresses are not ones a worker elsewhere can dial.
 */
export function suggestedAddresses(readiness: JoinReadiness): string[] {
  const out = new Set<string>();
  for (const { report } of readiness.coordinators) {
    if (!report || (report.environment ?? HostEnvironment.host) !== 'host') {
      continue;
    }
    for (const host of report.addresses) {
      out.add(joinHostPort(host, report.port));
    }
  }
  return [...out].filter((a) => !addressProblem(a));
}

// The address with its host in lowercase, so equal endpoints compare equal.
function normalized(address: string): string {
  const parts = splitHostPort(goTrimSpace(address));
  return parts ? joinHostPort(parts[0].toLowerCase(), parts[1]) : address;
}

// The endpoints the coordinators report: each advertised host and each
// interface address, with its coordinator's port.
function reportedEndpoints(readiness: JoinReadiness): string[] {
  return readiness.coordinators.flatMap(({ report }) =>
    report
      ? [report.advertise ?? '', ...report.addresses]
          .filter(Boolean)
          .map((host) => normalized(joinHostPort(host, report.port)))
      : []
  );
}

/**
 * The endpoint to offer in place of the field's address after the
 * coordinators' reports changed since step 2 started, as after a restart on
 * a new port: a newly reported endpoint with the field's host, else one
 * that replaced the field's address. Undefined while the field holds a
 * reported endpoint or the reports are as they were.
 */
export function movedEndpoint(
  address: string,
  opened: JoinReadiness,
  current: JoinReadiness
): string | undefined {
  const field = normalized(address);
  const before = reportedEndpoints(opened);
  const now = reportedEndpoints(current);
  if (now.includes(field) || now.join() === before.join()) return undefined;
  const host = splitHostPort(field)?.[0];
  const fresh = now.filter((e) => !before.includes(e));
  return (
    fresh.find((e) => splitHostPort(e)?.[0] === host) ??
    (before.includes(field) ? fresh[0] : undefined)
  );
}

// Splits host:port as Go's net.SplitHostPort does, or returns undefined.
function splitHostPort(address: string): [string, string] | undefined {
  const colon = address.lastIndexOf(':');
  if (colon < 0) return undefined;
  let host = address.slice(0, colon);
  if (address.startsWith('[')) {
    const end = address.indexOf(']');
    if (end < 0 || end + 1 !== colon) return undefined;
    host = address.slice(1, end);
  } else if (host.includes(':')) {
    return undefined;
  }
  if (/[[\]]/.test(host) || /[[\]]/.test(address.slice(colon + 1))) {
    return undefined;
  }
  return [host, address.slice(colon + 1)];
}

type ParsedIP = { v4: number[] } | { v6: number[] };

// Parses an IP address as Go's net.ParseIP does, without a zone. An IPv6
// address that maps an IPv4 one parses as that IPv4 address.
function parseIP(host: string): ParsedIP | undefined {
  if (IPV4.test(host)) return { v4: host.split('.').map(Number) };
  // Go reads only hex digits, colons, and dots; the URL parser would drop
  // a path or a zone.
  if (!host.includes(':') || !/^[0-9A-Fa-f:.]+$/.test(host)) return undefined;
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

const LISTEN =
  '{host} is a listen address. Enter an address that workers can dial.';
const LOOPBACK =
  '{host} reaches only the machine it runs on. Enter an address that workers on other hosts can reach.';
const NOT_ONE_MACHINE =
  '{host} is a multicast or broadcast address. Enter the address of one machine.';
const LINK_LOCAL =
  '{host} is a link-local address, which another network does not route. Enter another address of the machine.';

function ipProblem(ip: ParsedIP): string | undefined {
  if ('v4' in ip) {
    const [a, b] = ip.v4;
    if (ip.v4.every((n) => n === 0)) return LISTEN;
    if (a === 127) return LOOPBACK;
    if (a === 169 && b === 254) return LINK_LOCAL;
    if ((a ?? 0) >= 224 && (a ?? 0) <= 239) return NOT_ONE_MACHINE;
    if (ip.v4.every((n) => n === 255)) return NOT_ONE_MACHINE;
    return undefined;
  }
  const h = ip.v6;
  if (h.every((n) => n === 0)) return LISTEN;
  if (h.slice(0, 7).every((n) => n === 0) && h[7] === 1) return LOOPBACK;
  if (((h[0] ?? 0) & 0xffc0) === 0xfe80) return LINK_LOCAL;
  if (((h[0] ?? 0) & 0xff00) === 0xff00) return NOT_ONE_MACHINE;
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
 * What is wrong with an address, or undefined when a worker on another
 * machine can dial it. A problem never repeats an invite pasted into it.
 */
export function addressProblem(text: string): AddressProblem | undefined {
  // The MCP action trims the address before it checks it.
  const address = goTrimSpace(text);
  if (address === '') {
    return { text: 'Enter the address workers use to reach the coordinator.' };
  }
  if (holdsInvite(address)) {
    return {
      text: 'The address holds an invite. The invite goes only into the commands.',
    };
  }
  const parts = splitHostPort(address);
  if (!parts || parts[0] === '') {
    return { text: 'Enter host:port, such as 192.168.1.20:50055.' };
  }
  const [host, port] = parts;
  // Go's strconv.Atoi takes a sign.
  if (!/^[+-]?\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
    return { text: 'Enter a port from 1 to 65535.' };
  }
  const problem = hostProblem(host);
  return problem ? { text: problem, values: { host } } : undefined;
}
