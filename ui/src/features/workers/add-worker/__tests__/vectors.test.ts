// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The dialog and the MCP action must accept the same input and give the
// same commands. __fixtures__/commands.json holds cases that the MCP
// action's Go code produced; its Go tests read the same file, so a rule
// that drifts fails on both sides.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { addressProblem } from '../address';

interface Vector {
  name: string;
  input: {
    address: string;
    labels: string;
    reusable: boolean;
    version: string;
  };
  result: {
    accepted: boolean;
    field?: 'address' | 'labels';
    position?: number;
    image?: string;
    commands?: Record<string, string>;
  };
}

const FIXTURE = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../__fixtures__/commands.json'
);

const { cases } = JSON.parse(fs.readFileSync(FIXTURE, 'utf8')) as {
  cases: Vector[];
};

describe('shared vectors', () => {
  it.each(cases.map((c) => [c.name, c] as const))('address: %s', (_, c) => {
    const refused = c.result.field === 'address';
    expect(addressProblem(c.input.address) !== undefined).toBe(refused);
  });
});
