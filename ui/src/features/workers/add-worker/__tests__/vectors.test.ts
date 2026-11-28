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
import { addressProblem, goTrimSpace } from '../address';
import {
  normalizeLabels,
  publishedImage,
  workerCommand,
  WorkerTab,
} from '../commands';

interface Vector {
  name: string;
  input: {
    address: string;
    labels: string;
    reusable: boolean;
    version: string;
    inviteId: string;
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

const { cases, invite } = JSON.parse(fs.readFileSync(FIXTURE, 'utf8')) as {
  cases: Vector[];
  invite: string;
};

describe('shared vectors', () => {
  it.each(cases.map((c) => [c.name, c] as const))('address: %s', (_, c) => {
    const refused = c.result.field === 'address';
    expect(addressProblem(c.input.address) !== undefined).toBe(refused);
  });

  it.each(cases.map((c) => [c.name, c] as const))('labels: %s', (_, c) => {
    const labels = normalizeLabels(c.input.labels);
    if (c.result.field === 'labels') {
      expect('problem' in labels && labels.problem.values.position).toBe(
        c.result.position
      );
    } else {
      expect(labels).not.toHaveProperty('problem');
    }
  });

  // The dialog's Kubernetes tab always takes a reusable invite, so a
  // single-use case has no manifest to compare.
  it.each(
    cases.filter((c) => c.result.accepted).map((c) => [c.name, c] as const)
  )('commands: %s', (_, c) => {
    const image = publishedImage(c.input.version);
    expect(image ?? '').toBe(c.result.image ?? '');
    // The dialog uses the trimmed address and the normalized labels.
    const labels = normalizeLabels(c.input.labels);
    const input = {
      address: goTrimSpace(c.input.address),
      labels: 'labels' in labels ? labels.labels : '',
      inviteId: c.input.inviteId,
      invite,
      image,
    };
    const want = c.result.commands ?? {};
    expect(workerCommand(WorkerTab.linux, input)).toBe(want.linux);
    expect(workerCommand(WorkerTab.docker, input)).toBe(want.docker);
    if (c.input.reusable) {
      expect(workerCommand(WorkerTab.kubernetes, input)).toBe(want.kubernetes);
    }
  });
});
