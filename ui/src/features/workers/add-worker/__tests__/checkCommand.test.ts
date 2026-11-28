// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { spawnSync } from 'node:child_process';
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
function runCheck(shell: string, env: NodeJS.ProcessEnv, stubs = {}) {
  const bin = fs.mkdtempSync(path.join(os.tmpdir(), 'dagu-check-'));
  const all: Record<string, string> = { dagu: 'echo STARTED; env', ...stubs };
  for (const [name, body] of Object.entries(all)) {
    fs.writeFileSync(path.join(bin, name), `#!/bin/sh\n${body}\n`, {
      mode: 0o755,
    });
  }
  const command = checkCommand(WorkerTab.linux, {
    address: '192.168.1.20:50055',
    inviteId: 'inv-1',
  })!;
  const run = spawnSync(shell, ['-c', command], {
    encoding: 'utf8',
    env: { PATH: `${bin}:/usr/bin:/bin`, HOME: '/home/worker', ...env },
  });
  fs.rmSync(bin, { recursive: true, force: true });
  return run;
}

describe.skipIf(process.platform === 'win32')('Linux check command', () => {
  it.each(
    ['sh', 'bash'].flatMap((sh) => [[sh, true] as const, [sh, false] as const])
  )('in %s with proxies %s keeps them and no DAGU*', (shell, proxied) => {
    const { stdout } = runCheck(shell, {
      DAGU_HOME: '/home/worker/.dagu',
      DAGU_WORKER_INVITE: 'inherited',
      'DAGU_WORKER.INVITE': 'inherited',
      DAGU_DATA_DIR: '/var/lib/dagu',
      'DAGU_PATHS.DATA_DIR': '/var/lib/dagu',
      DAGU__DATA: '/var/lib/dagu',
      ...(proxied ? PROXIES : {}),
    });
    const vars = new Map(
      stdout
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

  // The new directory goes on exit, and the check's status stays.
  it.each(['sh', 'bash'])(
    'in %s removes its directory and keeps the status',
    (sh) => {
      const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'dagu-check-tmp-'));
      // A mktemp stub makes the directory where the test can see it.
      const mktemp = 'mkdir "$TMPDIR/home" && echo "$TMPDIR/home"';
      const run = runCheck(sh, { TMPDIR: tmp }, { dagu: 'exit 3', mktemp });
      const left = fs.readdirSync(tmp);
      fs.rmSync(tmp, { recursive: true, force: true });
      expect(run.status).toBe(3);
      expect(left).toEqual([]);
    }
  );

  // Without its directory, an empty DAGU_HOME would read the usual config.
  it.each(['sh', 'bash'])('in %s never starts Dagu when mktemp fails', (sh) => {
    const run = runCheck(sh, {}, { mktemp: 'exit 1' });
    expect(run.status).not.toBe(0);
    expect(run.stdout).not.toContain('STARTED');
  });
});
