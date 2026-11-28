// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { checkCommand, WorkerTab } from '../commands';

// The Linux check runs in a shell that may hold an enrolled worker's
// settings. A stub dagu on PATH prints the environment the check gets, so
// the test sees what the real CLI's config loader would read.
describe.skipIf(process.platform === 'win32')('Linux check command', () => {
  it.each(['sh', 'bash'])(
    'passes no inherited DAGU_* setting in %s',
    (shell) => {
      const bin = fs.mkdtempSync(path.join(os.tmpdir(), 'dagu-check-'));
      const stub = path.join(bin, 'dagu');
      fs.writeFileSync(stub, '#!/bin/sh\nenv\n');
      fs.chmodSync(stub, 0o755);
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
          DAGU_WORKER_INVITE: 'inherited',
          'DAGU_WORKER.INVITE': 'inherited',
          DAGU_DATA_DIR: '/var/lib/dagu',
          'DAGU_PATHS.DATA_DIR': '/var/lib/dagu',
        },
      });
      fs.rmSync(bin, { recursive: true, force: true });
      const names = out
        .trim()
        .split('\n')
        .map((line) => line.slice(0, line.indexOf('=')))
        .filter((name) => name.startsWith('DAGU'));
      expect(names).toEqual(['DAGU_HOME']);
      expect(out).toContain('HOME=/home/worker\n');
      expect(out).not.toContain('DAGU_HOME=/home/worker');
    }
  );
});
