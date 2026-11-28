// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  WorkerAccessReason,
  type components,
} from '@/api/v1/schema';
import { AddWorkerDialog } from '../AddWorker';

type Readiness = components['schemas']['WorkerJoinReadinessResponse'];
type Report = components['schemas']['JoinReadinessReport'];

const api = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', () => ({ useClient: () => api }));

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

function view(refreshFailed = false) {
  return <AddWorkerDialog refreshFailed={refreshFailed} onClose={() => {}} />;
}

// Settles the pending API calls and the renders they cause.
const settle = () => act(async () => {});

async function open(data: Readiness) {
  api.GET.mockResolvedValue(answer(200, data));
  const result = render(view());
  await settle();
  return result;
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

const writeText = vi.fn();

beforeEach(() => {
  api.GET.mockReset();
  writeText.mockReset().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  });
});

describe('AddWorker', () => {
  it('checks the coordinator while join readiness loads', () => {
    api.GET.mockReturnValue(new Promise(() => {}));
    render(view());

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

  // A restart can change the port; a default the admin did not touch
  // follows it, and a typed address stays.
  it('refreshes untouched defaults on a new reading only', async () => {
    const { rerender } = await open(
      loopback({ advertise: 'coord.example.com' })
    );
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      'coord.example.com:50055'
    );

    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise: 'coord.example.com', port: 50155 }))
    );
    fireEvent.focus(window);
    await settle();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(
      'coord.example.com:50155'
    );

    typeAddress('192.168.1.40:50055');
    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise: 'coord.example.com', port: 50255 }))
    );
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(3);
    expect(screen.getByLabelText(ADDRESS)).toHaveValue('192.168.1.40:50055');
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

  // Once no pod reports, the outside answer no longer applies: an untouched
  // field takes its default again, and a typed address stays.
  it.each([
    ['untouched', undefined, 'coord.example.com:50055'],
    ['typed', '203.0.113.7:30055', '203.0.113.7:30055'],
  ])('drops the placement when no pod reports, %s', async (_, typed, kept) => {
    const advertise = 'coord.example.com';
    const { rerender } = await open(readiness({ ...pod, advertise }));
    fireEvent.click(screen.getByRole('radio', { name: 'Outside the cluster' }));
    if (typed) typeAddress(typed);
    api.GET.mockResolvedValue(answer(200, readiness({ advertise })));
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(screen.queryByRole('radio')).toBeNull();
    expect(screen.queryByText(/assigned node port/)).toBeNull();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(kept);
  });

  // Inside starts with the pod's advertised address, never a last invite
  // address made for outside, and keeps it on a new reading. Without one,
  // the field starts empty and the hint asks for the address.
  it.each([
    ['advertises', pod, 'dagu-coordinator:50055', /short name below/],
    [
      'does not advertise',
      { ...pod, advertise: undefined },
      '',
      /^Enter the coordinator Service's address and port/,
    ],
  ])('fills Inside from the pod when it %s', async (_, r, value, hint) => {
    const data = {
      ...readings(r),
      lastInviteAddress: 'outside.example.test:30055',
    };
    const { rerender } = await open(data);
    fireEvent.click(screen.getByRole('radio', { name: 'Inside this cluster' }));
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(value);
    expect(screen.getByText(hint)).toBeInTheDocument();
    api.GET.mockResolvedValue(answer(200, data));
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(screen.getByLabelText(ADDRESS)).toHaveValue(value);
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
    expect(field).toHaveAccessibleDescription(/namespace-qualified name/);
    typeAddress('localhost:50055');
    expect(field).toHaveAccessibleDescription(
      /namespace-qualified name.*localhost/
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
    rerender(view(true));
    rerender(view(false));
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
    const c = { ...b, instanceId: 'c3', host: 'node-c' };
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
      /clear any peer.insecure: true or peer.skip_tls_verify: true.*clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate/,
    ],
    [503, WorkerAccessReason.coordinator_refused, /Its log names the reason/],
    [409, WorkerAccessReason.coordinator_conflict, /present state/],
    [503, WorkerAccessReason.retry_later, /Try again in a moment/],
  ])('names the cause of a %i (%s)', async (status, reason, text) => {
    api.GET.mockResolvedValue(answer(status, undefined, reason));
    render(view());
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

  // A focus read can answer before the read that opened the dialog; the
  // earlier read must not replace the newer answer.
  it('keeps the newest reading when answers arrive out of order', async () => {
    const first = deferred<unknown>();
    api.GET.mockReturnValueOnce(first.promise);
    render(view());
    api.GET.mockResolvedValueOnce(answer(200, readiness()));
    fireEvent.focus(window);
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
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Check again' })
    );
  });

  it('moves the focus of a removed copy button to the address', async () => {
    await open(loopback({ environment: HostEnvironment.container }));
    screen.getByRole('button', { name: 'Copy the change' }).focus();
    api.GET.mockResolvedValue(answer(200, readiness()));
    fireEvent.focus(window);
    await settle();
    expect(document.activeElement).toBe(screen.getByLabelText(ADDRESS));
  });

  it('leaves the focus where the admin put it on a new reading', async () => {
    const { rerender } = await open(readiness());
    const field = screen.getByLabelText(ADDRESS);
    field.focus();
    rerender(view(true));
    rerender(view(false));
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
  // restart, and on window focus until ready.
  it.each(['server', 'focus'])('reads readiness again on %s', async (by) => {
    const { rerender } = await open(loopback());
    if (by === 'server') rerender(view(true));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(1);

    api.GET.mockResolvedValue(answer(200, readiness()));
    if (by === 'server') rerender(view(false));
    else fireEvent.focus(window);
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Ready')).toBeInTheDocument();

    fireEvent.focus(window);
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);
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
  });
});
