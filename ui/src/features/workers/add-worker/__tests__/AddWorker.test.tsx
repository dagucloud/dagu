// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  type components,
} from '@/api/v1/schema';
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

function answer(status: number, data?: unknown, message?: string) {
  return {
    data,
    error: data ? undefined : { code: 'error', message },
    response: { status },
  };
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
    fireEvent.click(screen.getByRole('button', { name: '10.0.0.5' }));
    expect(screen.getByLabelText('Address')).toHaveValue('10.0.0.5');
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

  it.each([
    [403, undefined, 'Only admins can add workers.'],
    [409, undefined, /Upgrade every coordinator to add workers/],
    [
      503,
      'the server needs peer.token or the data directory',
      'the server needs peer.token or the data directory',
    ],
  ])('names the cause of a %i', async (status, message, text) => {
    api.GET.mockResolvedValue(answer(status, undefined, message));
    render(view());
    fireEvent.click(screen.getByRole('button', { name: 'Add worker' }));
    await settle();

    expect(screen.getByRole('alert')).toHaveTextContent(text);
    expect(
      screen.getByRole('button', { name: 'Check again' })
    ).toBeInTheDocument();
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

  it.each([
    ['localhost', '', 'localhost reaches only the machine it runs on'],
    ['127.0.0.1', '', '127.0.0.1 reaches only the machine it runs on'],
    ['0.0.0.0', '', '0.0.0.0 is a listen address'],
    ['http://192.168.1.20', '', 'without a scheme or path'],
    ['192.168.1.20:50055', '', 'Enter the port in the Port field.'],
    ['192.168.1.20', '70000', 'Enter a port from 1 to 65535.'],
    ['bad_host', '', 'bad_host is not a host name or IP address.'],
  ])('refuses the address %s', async (host, port, problem) => {
    await open(readiness());
    typeAddress(host, port || undefined);

    expect(
      screen.getByText(new RegExp(problem.replace(/[.:/]/g, '\\$&')))
    ).toBeInTheDocument();
    expect(
      step('Worker').getByText('Shown after steps 1 and 2')
    ).toBeInTheDocument();
  });

  it.each(['192.168.1.20', 'fd00::20', '[fd00::20]', 'coord.example.com'])(
    'accepts the address %s and moves on to step 3',
    async (host) => {
      await open(readiness());
      typeAddress(host, '30055');

      expect(screen.queryByText('Shown after steps 1 and 2')).toBeNull();
    }
  );

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
