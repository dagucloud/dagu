// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  WorkerAccessReason,
  WorkerConnection,
  WorkerHealthStatus,
  WorkerVersionDrift,
  type components,
} from '@/api/v1/schema';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { AddWorker } from '../AddWorker';

type Worker = components['schemas']['Worker'];
type Readiness = components['schemas']['WorkerJoinReadinessResponse'];
type Report = components['schemas']['JoinReadinessReport'];

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
const config = vi.hoisted(() => ({ version: '2.28.0' }));
const auth = vi.hoisted(() => ({ admin: true }));

vi.mock('@/hooks/api', () => ({ useClient: () => api }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: () => auth.admin }));

const INVITE = 'dagu1.pin.secret';

function report(overrides: Partial<Report> = {}): Report {
  return {
    listenHost: '0.0.0.0',
    port: 50055,
    configFile: '/home/me/.config/dagu/config.yaml',
    loopbackOnly: false,
    hostSource: { kind: ConfigSourceKind.default },
    environment: HostEnvironment.host,
    addresses: ['192.168.1.20', '10.0.0.5'],
    builtinTLS: true,
    ready: true,
    ...overrides,
  };
}

function readiness(
  overrides: Partial<Report> = {},
  lastInviteAddress?: string
): Readiness {
  return {
    coordinators: [
      {
        instanceId: 'c1',
        host: 'laptop',
        port: 50055,
        report: report(overrides),
      },
    ],
    lastInviteAddress,
  };
}

// A loopback-only coordinator is never ready.
function loopback(overrides: Partial<Report> = {}): Readiness {
  return readiness({
    listenHost: '127.0.0.1',
    loopbackOnly: true,
    addresses: [],
    ready: false,
    ...overrides,
  });
}

function answer(status: number, data?: unknown, reason?: string) {
  return {
    data,
    error: data
      ? undefined
      : {
          code: 'error',
          // A coordinator's own words, which the dialog never shows.
          message: 'set peer.token, or read /var/lib/dagu/peer-token',
          details: reason ? { reason } : undefined,
        },
    response: { status },
  };
}

// A promise the test settles when it chooses.
function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

// A worker as T16's /workers gives one that joined with an invite: a
// certificate connection that names its certificate and the invite.
function worker(overrides: Partial<Worker>): Worker {
  return {
    id: 'worker-a1',
    labels: {},
    totalPollers: 1,
    busyPollers: 0,
    runningTasks: [],
    lastHeartbeatAt: new Date().toISOString(),
    healthStatus: WorkerHealthStatus.healthy,
    version: '2.28.0',
    versionDrift: WorkerVersionDrift.none,
    connection: WorkerConnection.certificate,
    certificateId: 'cert-9',
    environment: HostEnvironment.host,
    ...overrides,
  };
}

function view(workers: Worker[] = [], refreshFailed = false) {
  return (
    <ConfigContext.Provider value={config as Config}>
      <AddWorker workers={workers} refreshFailed={refreshFailed} />
    </ConfigContext.Provider>
  );
}

// Settles the pending API calls and the renders they cause.
const settle = () => act(async () => {});

async function open(data: Readiness) {
  api.GET.mockResolvedValue(answer(200, data));
  const result = render(view());
  fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
  await settle();
  return result;
}

// The admin leaves the window and comes back, which fires focus and, for a
// hidden page, visibilitychange.
function comeBack(by: 'focus' | 'visibility' | 'both' = 'focus') {
  fireEvent.blur(window);
  if (by !== 'focus') fireEvent(document, new Event('visibilitychange'));
  if (by !== 'visibility') fireEvent.focus(window);
}

function step(title: string) {
  return within(screen.getByRole('region', { name: title }));
}

const ADDRESS = 'Address and port';
// Shaped like an invite, as an admin may paste one into the wrong field.
const PASTED_INVITE = `dagu1.${'A'.repeat(43)}.${'B'.repeat(43)}`;

function typeAddress(address: string) {
  fireEvent.change(screen.getByLabelText(ADDRESS), {
    target: { value: address },
  });
}

async function createInvite(expiresAt = '2026-11-29T10:00:00Z') {
  api.POST.mockResolvedValue(
    answer(201, { id: 'inv-1', invite: INVITE, expiresAt })
  );
  fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
  await settle();
}

function shownCommand(): string {
  return screen.getByText(/--worker\.invite=|DAGU_WORKER_INVITE/, {
    selector: 'pre',
  }).textContent!;
}

const writeText = vi.fn();

beforeEach(() => {
  api.GET.mockReset();
  api.POST.mockReset();
  auth.admin = true;
  config.version = '2.28.0';
  writeText.mockReset().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('AddWorker', () => {
  it('offers no button to a user who is not an admin', () => {
    auth.admin = false;
    render(view());
    expect(screen.queryByRole('button', { name: 'Add worker' })).toBeNull();
  });

  // Invites come only from the server the page is served by.
  it('offers Add worker only on the local server', () => {
    render(<RemoteNodeProvider remoteNode="prod">{view()}</RemoteNodeProvider>);
    expect(screen.getByRole('button', { name: 'Add worker' })).toBeDisabled();
    expect(
      screen.getByText('Switch to the local server to add a worker.')
    ).toBeInTheDocument();
  });

  it('checks the coordinator while join readiness loads', () => {
    api.GET.mockReturnValue(new Promise(() => {}));
    render(view());
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));

    expect(
      step('Accept workers from other hosts').getByText('Checking...')
    ).toBeInTheDocument();
    expect(screen.queryByLabelText(ADDRESS)).toBeNull();
    expect(api.GET).toHaveBeenCalledWith('/workers/join-readiness', {
      params: { query: { remoteNode: 'local' } },
    });
  });

  // Each suggestion carries the port of the coordinator that reported it.
  it('starts ready with the last invite address and suggests this host', async () => {
    const c2 = report({ port: 51055, addresses: ['10.0.0.9'] });
    await open({
      ...readiness({}, '192.168.1.30:50055'),
      coordinators: [
        { instanceId: 'c1', host: 'a', port: 50055, report: report() },
        { instanceId: 'c2', host: 'b', port: 51055, report: c2 },
      ],
    });

    expect(
      step('Accept workers from other hosts').getByText('Ready')
    ).toBeInTheDocument();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('192.168.1.30:50055');
    fireEvent.click(screen.getByRole('button', { name: '10.0.0.9:51055' }));
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('10.0.0.9:51055');
  });

  // A reading never rewrites the field. When the reported endpoint moved,
  // as with a restart on a new port, the dialog offers it, compared without
  // case, and Use this address takes it.
  it.each([
    ['the prefill', undefined],
    ['a typed address in capitals', 'COORD.EXAMPLE.COM:50055'],
  ])('offers a moved endpoint for %s', async (_, typed) => {
    const advertise = 'coord.example.com';
    const { rerender } = await open(readiness({ advertise }));
    if (typed) typeAddress(typed);
    const field = screen.getByLabelText(ADDRESS);
    const before = (field as HTMLTextAreaElement).value;
    expect(screen.queryByText(/now reports/)).toBeNull();

    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise, port: 50155 }))
    );
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(field).toHaveValue(before);
    expect(
      screen.getByText('The coordinator now reports coord.example.com:50155.')
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    expect(field).toHaveValue('coord.example.com:50155');
    expect(screen.queryByText(/now reports/)).toBeNull();
    typeAddress('COORD.EXAMPLE.COM:50155');
    expect(screen.queryByText(/now reports/)).toBeNull();
  });

  // After the admin takes an endpoint, a later move is offered from that
  // reading, and an endpoint the rule refuses is never offered.
  it('offers each move from the endpoint taken', async () => {
    const advertise = 'coord.example.com';
    const addresses: string[] = [];
    const { rerender } = await open(readiness({ advertise, addresses }));
    const reread = async (r: Readiness) => {
      api.GET.mockResolvedValue(answer(200, r));
      rerender(view(true));
      rerender(view(false));
      await settle();
    };
    await reread(readiness({ advertise, addresses, port: 50155 }));
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    await reread(readiness({ advertise, addresses }));
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(`${advertise}:50155`);
    expect(
      screen.getByText(`The coordinator now reports ${advertise}:50055.`)
    ).toBeInTheDocument();

    await reread(
      readiness({ advertise: `${advertise}.`, addresses, port: 50255 })
    );
    expect(screen.queryByText(/now reports/)).toBeNull();
  });

  // A suggestion taken also makes its reading the one later readings
  // compare with, as Use this address does.
  it('offers a move from a suggestion taken', async () => {
    const reading = (port: number) =>
      readiness({ port, addresses: ['192.168.1.20'] });
    const { rerender } = await open(reading(50055));
    const reread = async (port: number) => {
      api.GET.mockResolvedValue(answer(200, reading(port)));
      rerender(view(true));
      rerender(view(false));
      await settle();
    };
    await reread(50155);
    fireEvent.click(screen.getByRole('button', { name: '192.168.1.20:50155' }));
    await reread(50055);
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('192.168.1.20:50155');
    expect(
      screen.getByText('The coordinator now reports 192.168.1.20:50055.')
    ).toBeInTheDocument();
  });

  // A retained address stays on open, and a reading that still reports it,
  // or reports nothing new, offers nothing.
  it.each([
    ['a forwarded port', 'coord.example.com:51055', [50055], [50055]],
    [
      'a second coordinator',
      'coord.example.com:50055',
      [50055],
      [50055, 50155],
    ],
  ])('keeps a retained address with %s', async (_, last, first, next) => {
    const advertise = 'coord.example.com';
    const reading = (ports: number[]) => ({
      ...readings(...ports.map((port) => report({ advertise, port }))),
      lastInviteAddress: last,
    });
    const { rerender } = await open(reading(first));
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(last);
    api.GET.mockResolvedValue(answer(200, reading(next)));
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(last);
    expect(screen.queryByText(/now reports/)).toBeNull();
  });

  // The change goes to the source that set the host: a flag wins over a
  // variable, and a variable over a config file, such as admin.yaml over
  // config.yaml. In a container, -e still wins over a file.
  const { env, file, flag } = ConfigSourceKind;
  const { container, kubernetes } = HostEnvironment;
  const [V, F, A] = ['DAGU_COORDINATOR_HOST', '--coordinator.host', '/a.yaml'];
  const fileChange = `In ${A}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none.`;
  it.each([
    ['a variable', undefined, env, V, `${V}=0.0.0.0`],
    ['a flag', undefined, flag, F, `${F}=0.0.0.0`],
    ['a file', undefined, file, A, fileChange],
    ['a flag in a container', container, flag, F, '-p 50055:50055'],
    [
      'a file in a container',
      container,
      file,
      A,
      `-p 50055:50055 -e ${V}=0.0.0.0`,
    ],
    ['a flag in a pod', kubernetes, flag, F, `${F}=0.0.0.0`],
  ])(
    'follows %s that sets the listen host',
    async (_, environment, kind, name, change) => {
      await open(loopback({ environment, hostSource: { kind, name } }));
      expect(screen.getByText(change)).toBeInTheDocument();
      // A block to append could repeat the file's coordinator key.
      expect(
        screen.queryByText(/coordinator:/, { selector: 'pre' })
      ).toBeNull();
    }
  );

  it('says where to set the host for a coordinator on loopback only', async () => {
    await open(loopback());
    // Without a last invite or an advertised address, the field starts
    // empty, never with the browser's host.
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('');

    const step1 = step('Accept workers from other hosts');
    expect(step1.getByText('Needs change')).toBeInTheDocument();
    expect(
      step1.getByText(
        'The coordinator listens on 127.0.0.1, so only this machine can connect.'
      )
    ).toBeInTheDocument();
    expect(
      step1.getByText(
        /^In \/home\/me\/\.config\/dagu\/config\.yaml, set host to 0\.0\.0\.0/
      )
    ).toBeInTheDocument();
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
  });

  it('asks a container to publish its port and for the machine address', async () => {
    await open(loopback({ environment: HostEnvironment.container }));
    expect(
      screen.getByText('-p 50055:50055 -e DAGU_COORDINATOR_HOST=0.0.0.0', {
        selector: 'pre',
      })
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Enter the address of the machine that runs the container/
      )
    ).toBeInTheDocument();
    expect(screen.queryByText('Addresses of this machine:')).toBeNull();
  });

  it('keeps the port reminder for a ready container', async () => {
    await open(
      readiness({ environment: HostEnvironment.container, addresses: [] })
    );
    expect(
      screen.getByText(/Publish port 50055 for the container/)
    ).toBeInTheDocument();
    expect(screen.queryByText('Addresses of this machine:')).toBeNull();
  });

  // An upgrade cannot change an existing claim's access mode, so the dialog
  // names the prerequisite and the README instead of a snippet.
  it('states the Service requirement in Kubernetes', async () => {
    await open(loopback({ environment: HostEnvironment.kubernetes }));
    // The chart path names both settings helm template needs together.
    expect(
      screen.getByText(
        /^Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload.*shares its storage with the scheduler and the web UI.*worker pods keep their own.*set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany/
      )
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'README' })).toHaveAttribute(
      'href',
      expect.stringContaining('charts/dagu/README.md')
    );
    // No copyable chart values, which would skip moving the claim's data.
    expect(
      screen.queryByText(/deploymentMode|persistence/, { selector: 'pre' })
    ).toBeNull();
    // A worker outside the cluster dials a node, not the Service's name.
    fireEvent.click(screen.getByRole('radio', { name: 'Outside the cluster' }));
    expect(
      screen.getByText(/a node's address and the assigned node port/)
    ).toBeInTheDocument();
    expect(
      screen.getByText(/type LoadBalancer or NodePort/)
    ).toBeInTheDocument();
  });

  // A pod's advertised address is often a Service name only the cluster
  // resolves, so step 3 waits until the admin says where workers run.
  const pod = report({
    environment: HostEnvironment.kubernetes,
    advertise: 'dagu-coordinator',
    addresses: [],
  });
  const host = report({ port: 50155, addresses: ['192.168.1.20'] });
  const box = report({ environment: HostEnvironment.container, addresses: [] });
  const readings = (...reports: Report[]): Readiness => ({
    coordinators: reports.map((r, i) => ({
      instanceId: `c${i}`,
      host: `node-${i}`,
      port: r.port,
      report: r,
    })),
  });
  it.each([
    ['a pod', [pod]],
    ['a host, then a pod', [host, pod]],
    ['a pod, then a host', [pod, host]],
  ])('asks where workers run before step 3 for %s', async (_, reports) => {
    const user = userEvent.setup();
    await open(readings(...reports));
    // Suggestions come with the field, after the answer.
    const suggestion = () => screen.queryByText('Addresses of this machine:');
    expect(suggestion()).toBeNull();
    const radio = (name: string) => screen.getByRole('radio', { name });
    expect(radio('Inside this cluster')).not.toBeChecked();
    expect(radio('Outside the cluster')).not.toBeChecked();
    expect(screen.queryByLabelText(ADDRESS)).toBeNull();
    const worker = () =>
      step('Worker').queryByText('Shown after steps 1 and 2');
    expect(worker()).toBeInTheDocument();

    radio('Inside this cluster').focus();
    await user.keyboard(' ');
    expect(radio('Inside this cluster')).toBeChecked();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      'dagu-coordinator:50055'
    );
    expect(worker()).toBeNull();
    expect(suggestion() !== null).toBe(reports.includes(host));

    await user.keyboard('{ArrowDown}');
    expect(radio('Outside the cluster')).toBeChecked();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('');
    expect(screen.getByText(/assigned node port/)).toBeInTheDocument();
    expect(worker()).toBeInTheDocument();
    for (const refused of [' ', '127.0.0.1:30055']) {
      typeAddress(refused);
      expect(screen.getByLabelText(ADDRESS)).toHaveAttribute(
        'aria-invalid',
        'true'
      );
      expect(worker()).toBeInTheDocument();
    }
    typeAddress('203.0.113.7:30055');
    expect(worker()).toBeNull();
  });

  // Either placement's hint wins over a container listed before or after
  // the pod. Inside names the namespace-qualified Service address.
  it.each([
    ['Outside the cluster', 'a container, then a pod', [box, pod]],
    ['Outside the cluster', 'a pod, then a container', [pod, box]],
    ['Inside this cluster', 'a container, then a pod', [box, pod]],
    ['Inside this cluster', 'a pod, then a container', [pod, box]],
  ])('shows the hint for %s with %s', async (name, _, reports) => {
    await open(readings(...reports));
    fireEvent.click(screen.getByRole('radio', { name }));
    expect(
      screen.getByText(
        name === 'Outside the cluster'
          ? /assigned node port/
          : /dagu-coordinator\.<namespace>\.svc:50055/
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/runs the container/)).toBeNull();
  });

  // Once no pod reports, the outside answer no longer applies, and the
  // field stays as it is.
  it.each([
    ['untouched', undefined, ''],
    ['typed', '203.0.113.7:30055', '203.0.113.7:30055'],
  ])('drops the placement when no pod reports, %s', async (_, typed, kept) => {
    const advertise = 'coord.example.com';
    const { rerender } = await open(readiness({ ...pod, advertise }));
    fireEvent.click(screen.getByRole('radio', { name: 'Outside the cluster' }));
    if (typed) typeAddress(typed);
    api.GET.mockResolvedValue(answer(200, readiness({ advertise })));
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(
      screen.queryByRole('radio', { name: 'Outside the cluster' })
    ).toBeNull();
    expect(screen.queryByText(/assigned node port/)).toBeNull();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(kept);
  });

  // Inside starts with the pod's advertised address, never a last invite
  // address made for outside, and offers a new one a reading reports.
  // Without one, the field starts empty and the hint asks for the address.
  const qualified = 'coordinator.ops.svc.cluster.local';
  const reach = /^Check that worker pods can reach this address/;
  const ask = /^Enter the coordinator Service's address and port/;
  it.each([
    ['a short name', 'dagu-coordinator', 'dagu-coordinator-b', reach],
    ['a qualified name', qualified, `b.${qualified}`, reach],
    ['nothing', undefined, undefined, ask],
  ])('fills Inside from a pod advertising %s', async (_, first, next, hint) => {
    const data = (advertise?: string) => ({
      ...readings({ ...pod, advertise }),
      lastInviteAddress: 'outside.example.test:30055',
    });
    const { rerender } = await open(data(first));
    fireEvent.click(screen.getByRole('radio', { name: 'Inside this cluster' }));
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      first ? `${first}:50055` : ''
    );
    expect(screen.getByText(hint)).toBeInTheDocument();
    api.GET.mockResolvedValue(answer(200, data(next)));
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      first ? `${first}:50055` : ''
    );
    if (next) {
      expect(
        screen.getByText(`The coordinator now reports ${next}:50055.`)
      ).toBeInTheDocument();
    }
  });

  // The field's description carries the placement hint and any refusal,
  // so a keyboard user who tabs to it hears both.
  it('describes the field with the hint and the refusal', async () => {
    const user = userEvent.setup();
    await open(readings(pod));
    screen.getByRole('radio', { name: 'Inside this cluster' }).focus();
    await user.keyboard(' ');
    await user.tab();
    const field = screen.getByLabelText(ADDRESS);
    expect(document.activeElement).toBe(field);
    expect(field).toHaveAccessibleDescription(
      /namespace-qualified Service address/
    );
    typeAddress('localhost:50055');
    expect(field).toHaveAccessibleDescription(
      /namespace-qualified Service address.*localhost/
    );
  });

  // A reading that runs, as after a restart, holds step 3 until it
  // succeeds, also when the dialog was ready.
  it('holds step 3 while a reading runs', async () => {
    const { rerender } = await open(readiness({}, '192.168.1.30:50055'));
    const locked = () =>
      step('Worker').queryByText('Shown after steps 1 and 2');
    expect(locked()).toBeNull();
    const later = deferred<unknown>();
    api.GET.mockReturnValue(later.promise);
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(locked()).toBeInTheDocument();
    later.resolve(answer(200, readiness({}, '192.168.1.30:50055')));
    await settle();
    expect(locked()).toBeNull();
  });

  // Check again can remove its own button before the field shows; the focus
  // goes to the placement choice, which stays unanswered.
  it('moves the focus of Check again to the placement choice', async () => {
    const { environment } = pod;
    await open(loopback({ environment }));
    const check = screen.getByRole('button', { name: 'Check again' });
    check.focus();
    api.GET.mockResolvedValue(answer(200, readiness({ ...pod })));
    fireEvent.click(check);
    await settle();
    const inside = screen.getByRole('radio', { name: 'Inside this cluster' });
    expect(document.activeElement).toBe(inside);
    expect(inside).not.toBeChecked();
  });

  // A ready coordinator beside ones that refused or did not report keeps
  // the step, and so step 3, from being ready.
  it('names each coordinator and the error of one that did not report', async () => {
    const b = { instanceId: 'c2', host: 'node-b', port: 50055 };
    const c = { ...b, instanceId: 'c3', host: 'fd00::3' };
    const mismatch = WorkerAccessReason.peer_token_mismatch;
    await open({
      lastInviteAddress: '192.168.1.30:50055',
      coordinators: [
        { instanceId: 'c1', host: 'node-a', port: 50055, report: report() },
        { ...b, error: 'read /var/lib/dagu/peer-token' },
        { ...c, error: 'no match', errorReason: mismatch },
      ],
    });
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();

    expect(screen.getByText('node-b:50055')).toBeInTheDocument();
    expect(screen.getByText('[fd00::3]:50055')).toBeInTheDocument();
    expect(
      screen.getByText(
        'This coordinator did not report. Start or upgrade it, then check again.'
      )
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Set the same peer.token on both/)
    ).toBeInTheDocument();
    expect(screen.queryByText(/var\/lib/)).toBeNull();
    expect(screen.getByText('Needs change')).toBeInTheDocument();
  });

  // Removing a coordinator's own certificates alone leaves clients trusting
  // the old CA, so both paths link to the docs' steps.
  const OWN = ['Server TLS', 'Mutual TLS', 'Move to built-in TLS'];
  it.each([
    [BuiltinTLSReason.ownCertificates, /no built-in CA for invites/, OWN],
    [BuiltinTLSReason.caUnavailable, /could not create or read it/, []],
  ])('names why invites cannot work: %s', async (reason, text, links) => {
    await open(
      readiness({ builtinTLS: false, builtinTLSReason: reason, ready: false })
    );
    expect(screen.getByText(text)).toBeInTheDocument();
    expect(screen.getByText('Needs change')).toBeInTheDocument();
    const shown = screen.queryAllByRole('link');
    expect(shown.map((a) => a.textContent)).toEqual(links);
    expect(shown.map((a) => a.getAttribute('href')?.split('#')[1])).toEqual(
      links.length
        ? ['server-tls', 'mutual-tls', 'processes-on-another-host']
        : []
    );
  });

  // The dialog picks its words by details.reason and never shows the
  // server's message, which may say where a credential is kept.
  it.each([
    [403, undefined, 'Only admins can add workers.'],
    [403, WorkerAccessReason.coordinator_refused, 'Only admins can add'],
    [409, WorkerAccessReason.older_coordinator, /Upgrade every coordinator/],
    [409, undefined, /Upgrade every coordinator/],
    [503, WorkerAccessReason.no_coordinator, /No coordinator is running/],
    [503, undefined, /No coordinator is running/],
    [
      503,
      WorkerAccessReason.peer_token_missing,
      /Set peer.token on this server/,
    ],
    [503, WorkerAccessReason.peer_token_mismatch, /same peer.token on both/],
    [
      503,
      WorkerAccessReason.require_tls,
      /clear any peer.insecure: true or peer.skip_tls_verify: true.*clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate.*If these settings are in order, check that the network path to the coordinator passes TLS\./,
    ],
    [503, WorkerAccessReason.coordinator_refused, /Its log names the reason/],
    [409, WorkerAccessReason.coordinator_conflict, /present state/],
    [503, WorkerAccessReason.retry_later, /Try again in a moment/],
  ])('names the cause of a %i (%s)', async (status, reason, text) => {
    api.GET.mockResolvedValue(answer(status, undefined, reason));
    render(view());
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
    await settle();

    expect(screen.getByRole('alert')).toHaveTextContent(text);
    expect(screen.queryByText(/var\/lib/)).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Check again' })
    ).toBeInTheDocument();
    expect(
      step('Address workers use to reach the coordinator').getByText(
        'Shown after step 1'
      )
    ).toBeInTheDocument();
  });

  // A later read can answer before the read that opened the dialog; the
  // earlier read must not replace the newer answer.
  it('keeps the newest reading when answers arrive out of order', async () => {
    const first = deferred<unknown>();
    api.GET.mockReturnValueOnce(first.promise);
    const { rerender } = render(view());
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
    api.GET.mockResolvedValueOnce(answer(200, readiness()));
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(screen.getByText('Ready')).toBeInTheDocument();

    first.resolve(answer(503, undefined, WorkerAccessReason.no_coordinator));
    await settle();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // Any control a reading removes hands its focus to a control that stays.
  it('moves the focus of a removed suggestion to Check again', async () => {
    const { rerender } = await open(readiness());
    screen.getByRole('button', { name: '10.0.0.5:50055' }).focus();
    api.GET.mockResolvedValue(
      answer(503, undefined, WorkerAccessReason.no_coordinator)
    );
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Check again' })
    );
  });

  it('moves the focus of a removed copy button to the address', async () => {
    await open(loopback({ environment: HostEnvironment.container }));
    screen.getByRole('button', { name: 'Copy the change' }).focus();
    api.GET.mockResolvedValue(answer(200, readiness()));
    comeBack();
    await settle();
    expect(document.activeElement).toBe(screen.getByLabelText(ADDRESS));
  });

  it('leaves the focus where the admin put it on a new reading', async () => {
    const { rerender } = await open(readiness());
    const field = screen.getByLabelText(ADDRESS);
    field.focus();
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(document.activeElement).toBe(field);
  });

  // Only an address that passes the rule is offered or filled in.
  it('skips an unusable last invite address and suggestion', async () => {
    await open(
      readiness(
        {
          advertise: 'coord.example.com',
          addresses: ['192.168.1.20', '169.254.1.1'],
        },
        '127.0.0.1:50055'
      )
    );
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      'coord.example.com:50055'
    );
    expect(
      screen.queryByRole('button', { name: '169.254.1.1:50055' })
    ).toBeNull();
  });

  it('keeps the focus on Check again and announces Ready', async () => {
    await open(loopback());
    const check = screen.getByRole('button', { name: 'Check again' });
    check.focus();
    const answerLater = deferred<unknown>();
    api.GET.mockReturnValueOnce(answerLater.promise);
    fireEvent.click(check);
    await settle();
    expect(document.activeElement).toBe(check);
    expect(
      step('Accept workers from other hosts').getByRole('status')
    ).toHaveTextContent('Checking...');

    answerLater.resolve(answer(200, readiness()));
    await settle();
    expect(
      step('Accept workers from other hosts').getByRole('status')
    ).toHaveTextContent('Ready');
    expect(document.activeElement).toBe(screen.getByLabelText(ADDRESS));
  });

  // Readiness is read again when the workers refresh recovers, as after a
  // restart, and on window focus.
  it.each(['server', 'focus'])('reads readiness again on %s', async (by) => {
    const { rerender } = await open(loopback());
    if (by === 'server') rerender(view([], true));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(1);

    api.GET.mockResolvedValue(answer(200, readiness()));
    if (by === 'server') rerender(view([], false));
    else comeBack();
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  // A restart can change the port while the dialog shows Ready. Coming back
  // to the window reads again, holds step 3 meanwhile, and offers the port.
  it.each(['focus', 'visibility'] as const)(
    'rereads a ready dialog on %s',
    async (by) => {
      const advertise = 'coord.example.com';
      await open(readiness({ advertise }));
      expect(screen.getByLabelText(ADDRESS)).toHaveValue(`${advertise}:50055`);
      const locked = () =>
        step('Worker').queryByText('Shown after steps 1 and 2');
      expect(locked()).toBeNull();

      const later = deferred<unknown>();
      api.GET.mockReturnValue(later.promise);
      comeBack(by);
      await settle();
      expect(locked()).toBeInTheDocument();
      later.resolve(answer(200, readiness({ advertise, port: 50155 })));
      await settle();
      expect(screen.getByLabelText(ADDRESS)).toHaveValue(`${advertise}:50055`);
      expect(
        screen.getByText(`The coordinator now reports ${advertise}:50155.`)
      ).toBeInTheDocument();
      expect(locked()).toBeNull();
    }
  );

  // One return reads once for its focus and visibility events, and a
  // later return wins over a read still running from an earlier one.
  it('reads once per return, the newest winning', async () => {
    const advertise = 'coord.example.com';
    await open(readiness({ advertise }));
    const older = deferred<unknown>();
    api.GET.mockReturnValueOnce(older.promise);
    comeBack('both');
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);

    api.GET.mockResolvedValueOnce(
      answer(200, readiness({ advertise, port: 50155 }))
    );
    comeBack();
    await settle();
    older.resolve(answer(200, readiness({ advertise })));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(3);
    expect(
      screen.getByText(`The coordinator now reports ${advertise}:50155.`)
    ).toBeInTheDocument();
    expect(step('Worker').queryByText('Shown after steps 1 and 2')).toBeNull();
  });

  // The shared vectors in vectors.test.ts hold every address case; these
  // show how the dialog reacts to a refused and an accepted one.
  it.each([
    ['0.0.0.0:50055', '0.0.0.0 is a listen address'],
    ['', 'Enter the address workers use to reach the coordinator.'],
    ['worker.example:50\n055', 'Enter a port from 1 to 65535.'],
    ['[2001:db8::20/dashboard]:50055', 'is not one host'],
    ['224.0.0.1:50055', '224.0.0.1 is a multicast or broadcast address'],
    ['255.255.255.255:50055', 'is a multicast or broadcast address'],
    ['[ff02::1]:50055', 'ff02::1 is a multicast or broadcast address'],
    [`${PASTED_INVITE}:50055`, 'The address holds an invite.'],
  ])('refuses the address %s', async (address, problem) => {
    // A last invite address first, so that clearing it is a change.
    await open(readiness({}, '192.168.1.30:50055'));
    typeAddress(address);

    const field = screen.getByLabelText(ADDRESS);
    expect(field).toHaveAttribute('aria-invalid', 'true');
    expect(field).toHaveAccessibleDescription(expect.stringContaining(problem));
    expect(
      screen.queryByText(/AAAAAAAAAA/, { ignore: 'script, style, textarea' })
    ).toBeNull();
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create invite' })).toBeNull();
  });

  it.each(['192.168.1.20:30055', '[fd00::20]:50055', 'laptop:50055'])(
    'accepts the address %s and moves on to step 3',
    async (address) => {
      await open(readiness());
      typeAddress(address);

      expect(screen.queryByText('Shown after steps 1 and 2')).toBeNull();
      expect(
        screen.getByRole('button', { name: 'Create invite' })
      ).toBeEnabled();
    }
  );

  // vectors.test.ts holds the command text for every rule; these show that
  // the dialog creates the invite and copies exactly what it shows.
  it('creates a single-use invite and copies the Linux command', async () => {
    await open(readiness());
    typeAddress('[fd00::20]:50055');
    fireEvent.change(screen.getByLabelText('Labels (optional)'), {
      target: { value: 'gpu = true, team=data/eng' },
    });
    expect(screen.queryByText(/dagu1\./)).toBeNull();
    await createInvite();

    // Invites come only from the local server, which takes no remoteNode.
    expect(api.POST).toHaveBeenCalledWith('/workers/invites', {
      body: { reusable: false, address: '[fd00::20]:50055' },
    });
    const command =
      "dagu worker '--worker.coordinators=[fd00::20]:50055' " +
      `--worker.labels=gpu=true,team=data/eng --worker.invite=${INVITE}`;
    expect(shownCommand()).toBe(command);
    fireEvent.click(screen.getByRole('button', { name: 'Copy the command' }));
    await settle();
    expect(writeText).toHaveBeenCalledWith(command);
    // "Copied" names only the command it copied, not the next tab's.
    expect(
      screen.getByRole('button', { name: 'Command copied' })
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Docker' }));
    expect(shownCommand()).toMatch(/^docker run /);
    expect(screen.queryByRole('button', { name: 'Command copied' })).toBeNull();
  });

  // The whole ID names the objects, so IDs that share a prefix do not.
  it('gives Kubernetes a reusable invite named after it', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:30055');
    fireEvent.click(screen.getByRole('tab', { name: 'Kubernetes' }));
    expect(screen.queryByRole('radio')).toBeNull();
    const id = '3f2a9c1e-7b4d-4e8a-9c0f-1a2b3c4d5e6f';
    api.POST.mockResolvedValue(answer(201, { id, invite: INVITE }));
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    await settle();

    expect(api.POST).toHaveBeenCalledWith('/workers/invites', {
      body: { reusable: true, address: '192.168.1.20:30055' },
    });
    expect(shownCommand()).toContain(`  name: dagu-worker-invite-${id}\n`);
    expect(shownCommand()).toContain(`  name: dagu-worker-${id}\n`);
    expect(
      screen.getByText(/Reusable invite, until deleted./)
    ).toBeInTheDocument();
  });

  it('refuses a label by its position and holds the invite back', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.change(screen.getByLabelText('Labels (optional)'), {
      target: { value: `gpu=true,note=${PASTED_INVITE}` },
    });

    expect(
      screen.getByText(
        'Label 2 holds an invite. The invite goes only into the commands.'
      )
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/AAAAAAAAAA/, { ignore: 'script, style, textarea' })
    ).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Create invite' })
    ).toBeDisabled();
  });

  // The text-only panel takes the focus after its tab.
  it('offers no container command for a development build', async () => {
    const user = userEvent.setup();
    config.version = 'dev';
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.click(screen.getByRole('tab', { name: 'Docker' }));
    screen.getByRole('tab', { name: 'Docker' }).focus();
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole('tabpanel'));

    expect(
      screen.getByText(/no published image matches it/)
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create invite' })).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: 'Linux' }));
    expect(
      screen.getByRole('button', { name: 'Create invite' })
    ).toBeInTheDocument();
  });

  it('lets Linux choose a reusable invite', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.click(
      screen.getByRole('radio', { name: /Reusable: any number of hosts/ })
    );
    await createInvite('');

    expect(api.POST.mock.calls[0]?.[1]?.body).toEqual({
      reusable: true,
      address: '192.168.1.20:50055',
    });
  });

  // The tabs follow the ARIA tabs pattern: the arrow keys move between
  // them, and the panel is labelled by the selected tab.
  it('moves between tabs with the arrow keys', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    const linux = screen.getByRole('tab', { name: 'Linux' });
    linux.focus();
    fireEvent.keyDown(linux, { key: 'ArrowRight' });

    const docker = screen.getByRole('tab', { name: 'Docker' });
    expect(docker).toHaveAttribute('aria-selected', 'true');
    expect(document.activeElement).toBe(docker);
    expect(screen.getByRole('tabpanel')).toHaveAttribute(
      'aria-labelledby',
      docker.id
    );
  });

  // The dialog picks its words by details.reason, never the server's.
  it.each([
    [400, undefined, 'The server refused the address'],
    [403, undefined, 'Only admins can add workers.'],
    [409, WorkerAccessReason.own_certificates, /no built-in CA for invites/],
    [503, WorkerAccessReason.no_coordinator, /No coordinator is running/],
    [503, WorkerAccessReason.require_tls, /requires TLS/],
  ])(
    'names the cause when the invite fails with %i (%s)',
    async (status, reason, text) => {
      await open(readiness());
      typeAddress('192.168.1.20:50055');
      api.POST.mockResolvedValue(answer(status, undefined, reason));
      fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
      await settle();

      expect(screen.getByRole('alert')).toHaveTextContent(text);
      expect(screen.queryByText(/var\/lib/)).toBeNull();
      expect(screen.queryByText(/dagu1\./)).toBeNull();
    }
  );

  // A readiness read that fails after the invite was made keeps the invite
  // and its command on screen.
  it('keeps the invite when readiness fails later', async () => {
    const { rerender } = await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    const command = shownCommand();

    api.GET.mockResolvedValue(
      answer(503, undefined, WorkerAccessReason.no_coordinator)
    );
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(shownCommand()).toBe(command);

    api.GET.mockResolvedValue(answer(200, readiness()));
    fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
    await settle();
    expect(shownCommand()).toBe(command);
  });

  // The command dials the address the invite was made for, even when a new
  // reading or the admin changes the field while it is being created.
  // Escape while an invite is pending would drop the invite it returns.
  it('stays open until a pending invite settles', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    const created = deferred<unknown>();
    api.POST.mockReturnValue(created.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    await settle();
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    created.resolve(answer(201, { id: 'inv-1', invite: INVITE }));
    await settle();
    expect(shownCommand()).toContain(INVITE);
    // The first Escape closes the focused copy button's tooltip.
    for (let i = 0; i < 2; i++) {
      fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
      await settle();
    }
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  // A slow answer leaves the focus where the admin moved it meanwhile.
  it('keeps the focus the admin moved while an invite was pending', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    const created = deferred<unknown>();
    api.POST.mockReturnValue(created.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    const labels = screen.getByLabelText('Labels (optional)');
    labels.focus();
    fireEvent.change(labels, { target: { value: 'gpu=true' } });
    created.resolve(answer(201, { id: 'inv-1', invite: INVITE }));
    await settle();
    expect(document.activeElement).toBe(labels);
  });

  // An invite made while the admin switched to a tab of another kind is
  // announced there, with a way back to its command.
  it('points to an invite made on another tab', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.click(screen.getByRole('tab', { name: 'Kubernetes' }));
    const created = deferred<unknown>();
    api.POST.mockReturnValue(created.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Linux' }));
    created.resolve(answer(201, { id: 'inv-1', invite: INVITE }));
    await settle();
    expect(
      screen.getByText('The invite you created is on the Kubernetes tab.')
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show it' }));
    expect(screen.getByRole('tab', { name: 'Kubernetes' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(shownCommand()).toContain('kind: Secret');
    expect(screen.queryByText(/The invite you created/)).toBeNull();
  });

  // Create invite goes away with the new invite, so the keyboard focus
  // moves to the command's copy button, not back to the address.
  it('moves the focus to the new command', async () => {
    const user = userEvent.setup();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    api.POST.mockResolvedValue(answer(201, { id: 'inv-1', invite: INVITE }));
    screen.getByRole('button', { name: 'Create invite' }).focus();
    await user.keyboard('{Enter}');
    await settle();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Copy the command' })
    );
  });

  // A replacement invite for a new address keeps the first one and its
  // wait; going back to the first address shows the first invite again.
  it('keeps each invite and its own read', async () => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    const second = 'dagu1.second.invite';
    api.POST.mockResolvedValueOnce(answer(201, { id: 'a', invite: INVITE }));
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    await settle();
    typeAddress('192.168.1.21:50055');
    api.POST.mockResolvedValueOnce(answer(201, { id: 'b', invite: second }));
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    await settle();
    expect(shownCommand()).toContain(second);

    typeAddress('192.168.1.20:50055');
    expect(shownCommand()).toContain(`--worker.invite=${INVITE}`);
    api.GET.mockResolvedValue(answer(200, { workers: [], errors: [] }));
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    const reads = api.GET.mock.calls.filter(([path]) => path === '/workers');
    expect(reads).toHaveLength(2);
  });

  it('keeps the command on the address the invite was made for', async () => {
    const { rerender } = await open(
      readiness({ advertise: 'coord.example.com' })
    );
    const created = deferred<unknown>();
    api.POST.mockReturnValue(created.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));

    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise: 'coord.example.com', port: 50155 }))
    );
    rerender(view([], true));
    rerender(view([], false));
    await settle();
    created.resolve(answer(201, { id: 'inv-1', invite: INVITE }));
    await settle();

    expect(shownCommand()).toContain(
      '--worker.coordinators=coord.example.com:50055 '
    );
    expect(
      screen.getByText(
        'This invite was made for coord.example.com:50055. To use coord.example.com:50155, create another invite.'
      )
    ).toBeInTheDocument();
  });

  // A label may be 255 characters; jsdom cannot lay out, so the classes that
  // let it wrap in a narrow dialog stand in, checked at 390 px by hand.
  it('lets a long host name wrap in the notice and the details', async () => {
    vi.useFakeTimers();
    const host = `${'h'.repeat(63)}.example`;
    await open(readiness());
    typeAddress(`${host}:50055`);
    await createInvite();
    api.GET.mockResolvedValue(answer(200, { workers: [], errors: [] }));
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    typeAddress('192.168.1.20:50055');
    expect(
      screen.getByText(/This invite was made for/).closest('div')
    ).toHaveClass('break-words');
    expect(
      screen.getByText(`The worker cannot reach ${host}:50055.`).closest('div')
    ).toHaveClass('break-words');
  });

  it('lets a long worker ID and label wrap', async () => {
    const { rerender } = await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    const long = 'v' + 'x'.repeat(254);
    rerender(view([worker({ inviteId: 'inv-1', labels: { note: long } })]));
    expect(screen.getByText(`note=${long}`)).toHaveClass('break-all');
    expect(screen.getByText('worker-a1')).toHaveClass('break-all');
  });

  it('announces the worker that joins with the invite', async () => {
    const { rerender } = await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    const status = step('Worker').getByRole('status');
    expect(status).toHaveTextContent('Waiting for the worker to join...');

    rerender(
      view([
        worker({ id: 'other', inviteId: 'inv-0' }),
        worker({ inviteId: 'inv-1', labels: { gpu: 'true' } }),
      ])
    );

    // The same status region now says the worker joined.
    expect(status).toHaveTextContent('1 worker joined with this invite.');
    rerender(
      view([
        worker({ inviteId: 'inv-1' }),
        worker({ id: 'worker-b2', inviteId: 'inv-1' }),
      ])
    );
    expect(status).toHaveTextContent('2 workers joined with this invite.');
    expect(screen.getByText('worker-a1')).toBeInTheDocument();
    expect(screen.getByText('gpu=true')).toBeInTheDocument();
    expect(screen.queryByText('other')).toBeNull();
  });

  // A worker that joined stays joined, also after it leaves the list and
  // while another tab shows; the dialog's own read runs at most once.
  it('keeps a join and its read across tabs', async () => {
    vi.useFakeTimers();
    const { rerender } = await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    fireEvent.click(screen.getByRole('tab', { name: 'Kubernetes' }));
    rerender(view([worker({ inviteId: 'inv-1' })]));
    rerender(view([]));
    await act(async () => {
      vi.advanceTimersByTime(61_000);
    });
    fireEvent.click(screen.getByRole('tab', { name: 'Linux' }));

    expect(screen.getByText('worker-a1')).toBeInTheDocument();
    expect(screen.queryByText(/The usual causes/)).toBeNull();
    expect(api.GET).not.toHaveBeenCalledWith('/workers', expect.anything());
  });

  // With System status's refresh paused, the list the dialog gets is stale.
  // At 60 seconds the dialog reads the list once itself before it gives up.
  it('reads the workers once itself before it lists the usual causes', async () => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    api.GET.mockResolvedValue(
      answer(200, { workers: [worker({ inviteId: 'inv-1' })], errors: [] })
    );

    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    expect(api.GET).toHaveBeenCalledWith('/workers', {
      params: { query: { remoteNode: 'local' } },
    });
    expect(screen.getByText('worker-a1')).toBeInTheDocument();
    expect(screen.queryByText(/The usual causes/)).toBeNull();
  });

  // A read that fails or comes back partial cannot tell that nobody joined.
  it.each([
    [
      'a refused read',
      answer(503, undefined, WorkerAccessReason.no_coordinator),
    ],
    [
      'a partial list',
      answer(200, { workers: [], errors: ['coordinator down'] }),
    ],
  ])('says the list could not be read after %s', async (_, reply) => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    api.GET.mockResolvedValue(reply);

    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    expect(step('Worker').getByRole('status')).toHaveTextContent(
      /The workers list could not be read/
    );
    expect(screen.queryByText(/No worker has joined yet/)).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Look for the worker again' })
    ).toBeInTheDocument();
  });

  // The check reports how the host reaches the coordinator; it does not
  // read the invite yet.
  it.each([
    // An empty data directory and none of the shell's DAGU* variables, so
    // it dials after the worker joined too. checkCommand.test.ts runs it.
    [
      'Linux',
      `d="$(mktemp -d)" && (s=0; env $(env | sed -n 's/^\\(DAGU[A-Za-z0-9_.]*\\)=.*/-u \\1/p') DAGU_HOME="$d" dagu worker --check --worker.coordinators=192.168.1.20:50055 || s=$?; rm -rf "$d"; exit $s)`,
    ],
    [
      'Docker',
      'docker run --rm ghcr.io/dagucloud/dagu:2.28.0 dagu worker --check --worker.coordinators=192.168.1.20:50055',
    ],
    ['Kubernetes', 'kubectl logs deployment/dagu-worker-inv-1'],
  ])('lists the usual causes after 60 seconds on %s', async (tab, check) => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.click(screen.getByRole('tab', { name: tab }));
    await createInvite();
    api.GET.mockResolvedValue(answer(200, { workers: [], errors: [] }));

    await act(async () => {
      vi.advanceTimersByTime(59_000);
    });
    expect(screen.queryByText(/The usual causes/)).toBeNull();

    await act(async () => {
      vi.advanceTimersByTime(1_000);
    });
    expect(step('Worker').getByRole('status')).toHaveTextContent(
      'No worker has joined yet. The usual causes:'
    );
    expect(screen.getByText(/closes port 50055/)).toBeInTheDocument();
    expect(screen.getByText(/does not publish port 50055/)).toBeInTheDocument();
    expect(
      screen.getByText('The worker cannot reach 192.168.1.20:50055.')
    ).toBeInTheDocument();
    expect(screen.getByText(check, { selector: 'pre' })).toBeInTheDocument();
    // Each copy button names what it copies.
    for (const name of ['Copy the command', 'Copy the diagnostic command']) {
      fireEvent.click(screen.getByRole('button', { name }));
    }
    await settle();
    for (const name of ['Command copied', 'Diagnostic command copied']) {
      expect(screen.getAllByRole('button', { name })).toHaveLength(1);
    }
    // The check comes from the invite, so a label being corrected keeps it.
    fireEvent.change(screen.getByLabelText('Labels (optional)'), {
      target: { value: 'os=linux' },
    });
    expect(screen.getByText(check, { selector: 'pre' })).toBeInTheDocument();
  });

  // The field keeps line breaks as MCP reads them: one next to = is
  // trimmed off the key or value, so the command is one line, and one
  // inside a value is refused. vectors.test.ts holds the exact commands.
  it.each([
    ['gpu=\ntrue', ' --worker.labels=gpu=true '],
    ['gpu=\r\ntrue', ' --worker.labels=gpu=true '],
    ['zone=a\nb', undefined],
  ])('reads a line break in the labels %j', async (labels, argument) => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.change(screen.getByLabelText('Labels (optional)'), {
      target: { value: labels },
    });
    if (!argument) {
      expect(
        screen.getByText(/Label 1 breaks the worker label rule/)
      ).toBeInTheDocument();
      return;
    }
    await createInvite();
    expect(shownCommand()).toContain(argument);
  });

  // A second invite for a changed address checks the labels too.
  it('checks the labels before a replacement invite', async () => {
    const { rerender } = await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    typeAddress('192.168.1.21:50055');
    fireEvent.change(screen.getByLabelText('Labels (optional)'), {
      target: { value: 'gpu=true,,region=eu' },
    });
    rerender(view());

    expect(
      screen.getByRole('button', { name: 'Create invite' })
    ).toBeDisabled();
  });

  it('says to give another Docker worker its own volume', async () => {
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    fireEvent.click(screen.getByRole('tab', { name: 'Docker' }));
    await createInvite();
    expect(
      screen.getByText(/change the volume dagu-worker to one of its own/)
    ).toBeInTheDocument();
  });

  // The automatic read runs once per invite, also after the admin switches
  // tabs away and back once it ran.
  it('reads the workers once per invite across tab switches', async () => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    api.GET.mockResolvedValue(answer(200, { workers: [], errors: [] }));
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    fireEvent.click(screen.getByRole('tab', { name: 'Kubernetes' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Linux' }));
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });

    const reads = api.GET.mock.calls.filter(([path]) => path === '/workers');
    expect(reads).toHaveLength(1);
    expect(screen.getByText(/The usual causes/)).toBeInTheDocument();
  });

  // The retry keeps focus while it reads and when the result switches
  // between a complete miss and a failed read. A join removes it, so its
  // focus moves to the status that announces the join.
  const complete = { workers: [], errors: [] };
  const partial = { workers: [], errors: ['node b is unreachable'] };
  const joinedList = { ...complete, workers: [worker({ inviteId: 'inv-1' })] };
  it.each([
    ['a miss to a failed read', complete, partial],
    ['a failed read to a miss', partial, complete],
    ['a miss to a join', complete, joinedList],
  ])('keeps the focus on the retry from %s', async (_, first, next) => {
    vi.useFakeTimers();
    await open(readiness());
    typeAddress('192.168.1.20:50055');
    await createInvite();
    api.GET.mockResolvedValue(answer(200, first));
    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    const retry = screen.getByRole('button', {
      name: 'Look for the worker again',
    });
    retry.focus();
    const later = deferred<unknown>();
    api.GET.mockReturnValue(later.promise);
    fireEvent.click(retry);
    await act(async () => {});
    expect(document.activeElement).toBe(retry);
    later.resolve(answer(200, next));
    await act(async () => {});
    if (next === joinedList) {
      expect(document.activeElement).toBe(step('Worker').getByRole('status'));
      return;
    }
    expect(document.activeElement).toBe(retry);
    expect(
      screen.getByText(
        next === complete ? /The usual causes/ : /could not be read/
      )
    ).toBeInTheDocument();
  });
});
