// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { checkCommand, WorkerTab } from '../commands';

// Each proxy variable the dial reads, in both spellings.
const PROXIES = Object.fromEntries(
  ['HTTPS_PROXY', 'HTTP_PROXY', 'NO_PROXY'].flatMap((n) =>
    [n, n.toLowerCase()].map((k) => [k, 'http://proxy.example:3128'])
  )
);

// The Linux check runs in a shell that may hold an enrolled worker's
// settings. A stub dagu on PATH prints the environment the check gets, so
// the test sees what the real CLI's config loader and proxy lookup read.
describe.skipIf(process.platform === 'win32')('Linux check command', () => {
  it.each(
    ['sh', 'bash'].flatMap((sh) => [[sh, true] as const, [sh, false] as const])
  )('in %s with proxies %s keeps them and no DAGU*', (shell, proxied) => {
    const bin = fs.mkdtempSync(path.join(os.tmpdir(), 'dagu-check-'));
    fs.writeFileSync(path.join(bin, 'dagu'), '#!/bin/sh\nenv\n', {
      mode: 0o755,
    });
    const command = checkCommand(WorkerTab.linux, {
      address: '192.168.1.20:50055',
      labels: '',
      inviteId: 'inv-1',
      invite: 'unused',
    })!;
    const out = execFileSync(shell, ['-c', command], {
      encoding: 'utf8',
      env: {
        PATH: `${bin}:/usr/bin:/bin`,
        HOME: '/home/worker',
        DAGU_HOME: '/home/worker/.dagu',
        DAGU_WORKER_INVITE: 'inherited',
        'DAGU_WORKER.INVITE': 'inherited',
        DAGU_DATA_DIR: '/var/lib/dagu',
        'DAGU_PATHS.DATA_DIR': '/var/lib/dagu',
        DAGU__DATA: '/var/lib/dagu',
        ...(proxied ? PROXIES : {}),
      },
    });
    fs.rmSync(bin, { recursive: true, force: true });
    const vars = new Map(
      out
        .trim()
        .split('\n')
        .map((line) => [line.slice(0, line.indexOf('=')), line] as const)
    );
    const dagu = [...vars.keys()].filter((name) => name.startsWith('DAGU'));
    expect(dagu).toEqual(['DAGU_HOME']);
    expect(vars.get('DAGU_HOME')).not.toBe('DAGU_HOME=/home/worker/.dagu');
    expect(vars.get('HOME')).toBe('HOME=/home/worker');
    for (const [name, value] of Object.entries(PROXIES)) {
      expect(vars.get(name)).toBe(proxied ? `${name}=${value}` : undefined);
    }
  });
});
