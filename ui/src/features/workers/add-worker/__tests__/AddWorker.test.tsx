// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  WorkerAccessReason,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { AddWorker } from '../AddWorker';

type Readiness = components['schemas']['WorkerJoinReadinessResponse'];
type Report = components['schemas']['JoinReadinessReport'];

const api = vi.hoisted(() => ({ GET: vi.fn() }));
const auth = vi.hoisted(() => ({ admin: true }));

vi.mock('@/hooks/api', () => ({ useClient: () => api }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: () => auth.admin }));

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
  return <AddWorker refreshFailed={refreshFailed} />;
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

function step(title: string) {
  return within(screen.getByRole('region', { name: title }));
}

function typeAddress(host: string, port?: string) {
  fireEvent.change(screen.getByLabelText('Address'), {
    target: { value: host },
  });
  if (port !== undefined) {
    fireEvent.change(screen.getByLabelText('Port'), {
      target: { value: port },
    });
  }
}

const writeText = vi.fn();

beforeEach(() => {
  api.GET.mockReset();
  auth.admin = true;
  writeText.mockReset().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
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
    expect(screen.queryByLabelText('Address')).toBeNull();
    expect(api.GET).toHaveBeenCalledWith('/workers/join-readiness', {
      params: { query: { remoteNode: 'local' } },
    });
  });

  it('starts ready with the last invite address and suggests this host', async () => {
    await open(readiness({}, '192.168.1.30:50055'));

    expect(
      step('Accept workers from other hosts').getByText('Ready')
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Address')).toHaveValue('192.168.1.30');
    expect(screen.getByLabelText('Port')).toHaveValue('50055');
    fireEvent.click(screen.getByRole('button', { name: '10.0.0.5:50055' }));
    expect(screen.getByLabelText('Address')).toHaveValue('10.0.0.5');
  });

  // Each suggestion carries the port of the coordinator that reported it.
  it('pairs each suggested host with its coordinator port', async () => {
    await open({
      coordinators: [
        { instanceId: 'c1', host: 'a', port: 50055, report: report() },
        {
          instanceId: 'c2',
          host: 'b',
          port: 51055,
          report: report({ port: 51055, addresses: ['10.0.0.9'] }),
        },
      ],
    });
    fireEvent.click(screen.getByRole('button', { name: '10.0.0.9:51055' }));

    expect(screen.getByLabelText('Address')).toHaveValue('10.0.0.9');
    expect(screen.getByLabelText('Port')).toHaveValue('51055');
  });

  // A restart can change the port; a default the admin did not touch
  // follows it, and a typed address stays.
  it('refreshes untouched defaults on a new reading only', async () => {
    const { rerender } = await open(
      loopback({ advertise: 'coord.example.com' })
    );
    expect(screen.getByLabelText('Port')).toHaveValue('50055');

    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise: 'coord.example.com', port: 50155 }))
    );
    fireEvent.focus(window);
    await settle();
    expect(screen.getByLabelText('Port')).toHaveValue('50155');

    typeAddress('192.168.1.40');
    api.GET.mockResolvedValue(
      answer(200, readiness({ advertise: 'coord.example.com', port: 50255 }))
    );
    rerender(view(true));
    rerender(view(false));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(3);
    expect(screen.getByLabelText('Address')).toHaveValue('192.168.1.40');
    expect(screen.getByLabelText('Port')).toHaveValue('50155');
  });

  it('starts with an explicit advertise, else empty and never the browser host', async () => {
    await open(readiness({ advertise: 'coord.example.com' }));
    expect(screen.getByLabelText('Address')).toHaveValue('coord.example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await settle();

    // Each opening reads readiness afresh.
    api.GET.mockResolvedValue(answer(200, readiness()));
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
    await settle();
    expect(window.location.host).not.toBe('');
    expect(screen.getByLabelText('Address')).toHaveValue('');
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
  });

  // admin.yaml is applied after config.yaml, so the change goes where the
  // value came from.
  it('points to the file that set the listen host', async () => {
    await open(
      loopback({
        hostSource: {
          kind: ConfigSourceKind.file,
          name: '/home/me/.config/dagu/admin.yaml',
        },
      })
    );
    expect(
      screen.getByText(
        '/home/me/.config/dagu/admin.yaml sets coordinator.host. Change it there to 0.0.0.0:'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/config\.yaml:$/)).toBeNull();
  });

  // A flag wins over the variable, so a container started with one is told
  // to change it, and still to publish the port.
  it('names the flag that pins a container to loopback', async () => {
    await open(
      loopback({
        environment: HostEnvironment.container,
        hostSource: { kind: ConfigSourceKind.flag, name: '--coordinator.host' },
      })
    );
    expect(
      screen.getByText(/The flag --coordinator.host sets coordinator.host/)
    ).toBeInTheDocument();
    expect(
      screen.getByText('-p 50055:50055', { selector: 'pre' })
    ).toBeInTheDocument();
  });

  it('shows the config file lines for a coordinator on loopback only', async () => {
    await open(loopback());

    const step1 = step('Accept workers from other hosts');
    expect(step1.getByText('Needs change')).toBeInTheDocument();
    expect(
      step1.getByText(
        'The coordinator listens on 127.0.0.1, so only this machine can connect.'
      )
    ).toBeInTheDocument();
    expect(
      step1.getByText('Add these lines to /home/me/.config/dagu/config.yaml:')
    ).toBeInTheDocument();
    fireEvent.click(step1.getByRole('button', { name: 'Copy the lines' }));
    await settle();
    expect(writeText).toHaveBeenCalledWith('coordinator:\n  host: 0.0.0.0');
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
  });

  it.each([
    [
      ConfigSourceKind.env,
      'DAGU_COORDINATOR_HOST',
      'DAGU_COORDINATOR_HOST=0.0.0.0',
    ],
    [ConfigSourceKind.flag, '--coordinator.host', '--coordinator.host=0.0.0.0'],
  ])('shows the %s that sets the listen host', async (kind, name, setting) => {
    await open(loopback({ hostSource: { kind, name } }));
    expect(screen.getByText(setting, { selector: 'pre' })).toBeInTheDocument();
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

  it('states the Service requirement in Kubernetes', async () => {
    await open(loopback({ environment: HostEnvironment.kubernetes }));
    expect(
      screen.getByText(
        /Set coordinator.host to 0.0.0.0 through the chart's values/
      )
    ).toBeInTheDocument();
    expect(
      screen.getByText(/type LoadBalancer or NodePort/)
    ).toBeInTheDocument();
  });

  // One coordinator that does not answer keeps the step from being ready.
  it('names each coordinator and the error of one that did not report', async () => {
    await open({
      coordinators: [
        { instanceId: 'c1', host: 'node-a', port: 50055, report: report() },
        {
          instanceId: 'c2',
          host: 'node-b',
          port: 50055,
          error: 'set peer.token, or read /var/lib/dagu/peer-token',
        },
        {
          instanceId: 'c3',
          host: 'node-c',
          port: 50055,
          error: 'the peer token does not match',
          errorReason: WorkerAccessReason.peer_token_mismatch,
        },
      ],
    });

    expect(screen.getByText('node-a:50055')).toBeInTheDocument();
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

  it.each([
    [BuiltinTLSReason.ownCertificates, /uses peer.cert_file and peer.key_file/],
    [BuiltinTLSReason.caUnavailable, /could not create or read it/],
  ])('names why invites cannot work: %s', async (reason, text) => {
    await open(
      readiness({ builtinTLS: false, builtinTLSReason: reason, ready: false })
    );
    expect(screen.getByText(text)).toBeInTheDocument();
    expect(screen.getByText('Needs change')).toBeInTheDocument();
  });

  // The dialog picks its words by details.reason and never shows the
  // server's message, which may say where a credential is kept.
  it.each([
    [403, undefined, 'Only admins can add workers.'],
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
    [503, WorkerAccessReason.require_tls, /requires TLS/],
    [503, WorkerAccessReason.coordinator_refused, /Its log names the reason/],
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
  });

  // A focus read can answer before the read that opened the dialog; the
  // earlier read must not replace the newer answer.
  it('keeps the newest reading when answers arrive out of order', async () => {
    const first = deferred<unknown>();
    api.GET.mockReturnValueOnce(first.promise);
    render(view());
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
    api.GET.mockResolvedValueOnce(answer(200, readiness()));
    fireEvent.focus(window);
    await settle();
    expect(screen.getByText('Ready')).toBeInTheDocument();

    first.resolve(answer(503, undefined, WorkerAccessReason.no_coordinator));
    await settle();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // Check again keeps the focus while it checks, hands it to the address
  // when it leaves, and the step's state is announced.
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
    expect(document.activeElement).toBe(screen.getByLabelText('Address'));
  });

  // The workers refresh fails while Dagu restarts; its recovery is the only
  // trigger, besides opening, to read readiness again.
  it('reads readiness again when the server is back', async () => {
    const { rerender } = await open(loopback());
    rerender(view(true));
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(1);

    api.GET.mockResolvedValue(answer(200, readiness()));
    rerender(view(false));
    await settle();

    expect(api.GET).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });

  it('reads readiness again when the window regains focus, until ready', async () => {
    await open(loopback());
    api.GET.mockResolvedValue(answer(200, readiness()));
    fireEvent.focus(window);
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Ready')).toBeInTheDocument();

    fireEvent.focus(window);
    await settle();
    expect(api.GET).toHaveBeenCalledTimes(2);
  });

  // The cases of the MCP action's TestAddWorkerRefusesAddress, with the
  // host and the port in their own fields.
  it.each([
    ['0.0.0.0', '', '0.0.0.0 is a listen address'],
    ['[::]', '', ':: is a listen address'],
    ['127.0.0.1', '', '127.0.0.1 reaches only the machine it runs on'],
    ['[::1]', '', '::1 reaches only the machine it runs on'],
    ['localhost', '', 'localhost reaches only the machine it runs on'],
    ['192.168.1.20', '0', 'Enter a port from 1 to 65535.'],
    ['3f4e5a6b7c8d', '', '3f4e5a6b7c8d looks like a container ID'],
    ['[::1%lo0]', '', '::1%lo0 is not one host'],
    ['[fe80::1%eth0]', '', 'fe80::1%eth0 is not one host'],
    ['[fe80::1]', '', 'fe80::1 is a link-local address'],
    ['169.254.10.20', '', '169.254.10.20 is a link-local address'],
    ['host1,host2', '', 'host1,host2 is not one host'],
    ['$(DAGU_WORKER_INVITE)', '', '$(DAGU_WORKER_INVITE) is not one host'],
    ['::ffff:127.0.0.1', '', '::ffff:127.0.0.1 reaches only the machine'],
    ['bad:name', '', 'bad:name is not one host'],
    ['worker..example', '', 'worker..example is not one host'],
    ['http://192.168.1.20', '', 'is not one host'],
    ['0.0.0.0:50055', '', '0.0.0.0 is a listen address'],
    ['192.168.1.20:50055', '', 'Enter the port in the Port field.'],
    ['localhost.', '', 'localhost. reaches only the machine it runs on'],
    ['192.168.1.20', '70000', 'Enter a port from 1 to 65535.'],
  ])('refuses the address %s %s', async (host, port, problem) => {
    await open(readiness());
    typeAddress(host, port || undefined);

    expect(screen.getByText(problem, { exact: false })).toBeInTheDocument();
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
  });

  it('marks the field that is wrong and links it to the problem', async () => {
    await open(readiness());
    typeAddress('192.168.1.20', '70000');

    const port = screen.getByLabelText('Port');
    expect(port).toHaveAttribute('aria-invalid', 'true');
    expect(port).toHaveAccessibleDescription('Enter a port from 1 to 65535.');
    expect(screen.getByLabelText('Address')).not.toHaveAttribute(
      'aria-invalid'
    );
  });

  it.each([
    '192.168.1.20',
    'fd00::20',
    '[fd00::20]',
    'coord.example.com',
    'laptop',
    'worker_1.example',
  ])('accepts the address %s and moves on to step 3', async (host) => {
    await open(readiness());
    typeAddress(host, '30055');

    expect(screen.queryByText('Shown after steps 1 and 2')).toBeNull();
  });

  it('requires the address once it is cleared', async () => {
    await open(readiness({}, '192.168.1.30:50055'));
    typeAddress('');
    expect(
      screen.getByText(
        'Enter the address workers use to reach the coordinator.'
      )
    ).toBeInTheDocument();
  });
});
