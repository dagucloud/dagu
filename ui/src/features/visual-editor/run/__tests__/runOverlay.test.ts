// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NodeStatus,
  NodeStatusLabel,
  ReusedInputVia,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { overlaySample } from '../../testing/fixtures';
import { buildRunOverlay } from '../runOverlay';

type Node = components['schemas']['Node'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

function node(name: string, fields: Partial<Node>): Node {
  return {
    step: { name } as Node['step'],
    stdout: '',
    stderr: '',
    startedAt: '-',
    finishedAt: '-',
    status: NodeStatus.NotStarted,
    statusLabel: NodeStatusLabel.not_started,
    retryCount: 0,
    doneCount: 0,
    ...fields,
  };
}

const sampleNodes: Node[] = [
  node('fetch', {
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    startedAt: '2026-01-15T02:00:00Z',
    finishedAt: '2026-01-15T02:00:03Z',
    doneCount: 1,
    error: '',
  }),
  node('transform', {
    status: NodeStatus.Failed,
    statusLabel: NodeStatusLabel.failed,
    startedAt: '2026-01-15T02:00:03Z',
    finishedAt: '2026-01-15T02:00:04Z',
    retryCount: 2,
    doneCount: 1,
    error: 'exit status 1',
  }),
  node('publish', {
    status: NodeStatus.Running,
    statusLabel: NodeStatusLabel.running,
    startedAt: '2026-01-15T02:00:05Z',
    finishedAt: '-',
  }),
];

function details(fields: Partial<DAGRunDetails>): DAGRunDetails {
  return {
    dagRunId: 'r1',
    name: 'dag',
    status: Status.Running,
    statusLabel: StatusLabel.running,
    nodes: [],
    ...fields,
  } as DAGRunDetails;
}

describe('buildRunOverlay', () => {
  it('maps nodes by server step name', () => {
    expect(buildRunOverlay(details({ nodes: sampleNodes }))).toEqual(
      overlaySample
    );
  });

  it('omits placeholder times and empty errors', () => {
    const overlay = buildRunOverlay(
      details({ nodes: [node('a', { startedAt: '', error: '' })] })
    );
    expect(overlay.a).not.toHaveProperty('startedAt');
    expect(overlay.a).not.toHaveProperty('finishedAt');
    expect(overlay.a).not.toHaveProperty('error');
  });

  it('includes handler nodes', () => {
    const overlay = buildRunOverlay(
      details({
        nodes: [],
        onFailure: node('onFailure', {
          status: NodeStatus.Success,
          statusLabel: NodeStatusLabel.succeeded,
          startedAt: '2026-01-15T02:00:06Z',
        }),
        onExit: node('onExit', {}),
      })
    );
    expect(Object.keys(overlay).sort()).toEqual(['onExit', 'onFailure']);
    expect(overlay.onFailure?.status).toBe(NodeStatus.Success);
    expect(overlay.onFailure?.startedAt).toBe('2026-01-15T02:00:06Z');
  });

  // The card of a step that the run did not execute reads as reused, not as
  // never run: its state carries where the outputs came from.
  it('marks a skipped step whose outputs the run reused, from the record of the server', () => {
    const overlay = buildRunOverlay(
      details({
        reusedInputs: [
          {
            step: 'fetch',
            runId: 'RunA',
            capturedAt: '1970-01-01T00:00:05Z',
            via: ReusedInputVia.cache,
          },
        ],
        nodes: [
          node('fetch', { status: NodeStatus.Skipped }),
          node('other', { status: NodeStatus.Skipped }),
        ],
      })
    );
    expect(overlay.fetch?.status).toBe(NodeStatus.Skipped);
    expect(overlay.fetch?.reused).toMatchObject({
      step: 'fetch',
      source: 'cache',
      runId: 'RunA',
      capturedAt: 5000,
    });
    expect(overlay.other).not.toHaveProperty('reused');
  });

  it('reads the labels of a run that has no record', () => {
    const overlay = buildRunOverlay(
      details({
        labels: ['dagu.input=cache/5/fetch'],
        nodes: [node('fetch', { status: NodeStatus.Skipped })],
      })
    );
    expect(overlay.fetch?.reused).toMatchObject({
      source: 'cache',
      capturedAt: 5000,
    });
  });

  it('derives the source of an older run from its outputs-from label', () => {
    const overlay = buildRunOverlay(
      details({
        labels: ['dagu.outputs-from=r0'],
        nodes: [
          node('fetch', {
            status: NodeStatus.Skipped,
            doneCount: 1,
            finishedAt: '2026-01-15T02:00:03Z',
          }),
        ],
      })
    );
    expect(overlay.fetch?.reused).toMatchObject({ source: 'run', runId: 'r0' });
  });

  // A step run skips every step that it does not execute. A skipped step
  // that carries no outputs is a step that the source run could not give.
  it('marks a skipped step of a run with a source that gave no outputs', () => {
    const overlay = buildRunOverlay(
      details({
        labels: ['dagu.outputs-from=r0'],
        nodes: [
          node('fetch', {
            status: NodeStatus.Skipped,
            doneCount: 1,
            finishedAt: '2026-01-15T02:00:03Z',
          }),
          node('extract', { status: NodeStatus.Skipped }),
          node('load', { status: NodeStatus.Success }),
        ],
      })
    );
    expect(overlay.fetch?.reuseFailed).toBeUndefined();
    expect(overlay.extract?.reuseFailed).toEqual({ runId: 'r0' });
    expect(overlay.load?.reuseFailed).toBeUndefined();
  });

  // The server writes the labels in lower case; the record has the id as it is.
  it('names the source run as the record writes it', () => {
    const overlay = buildRunOverlay(
      details({
        labels: ['dagu.outputs-from=034nabc'],
        reusedInputs: [
          {
            step: 'fetch',
            runId: '034nAbC',
            via: ReusedInputVia.run,
          },
        ],
        nodes: [
          node('fetch', {
            status: NodeStatus.Skipped,
            doneCount: 1,
            finishedAt: '2026-01-15T02:00:03Z',
          }),
          node('extract', { status: NodeStatus.Skipped }),
        ],
      })
    );
    expect(overlay.extract?.reuseFailed).toEqual({ runId: '034nAbC' });
  });

  it('does not mark a skipped step of a run with no source', () => {
    const overlay = buildRunOverlay(
      details({ nodes: [node('extract', { status: NodeStatus.Skipped })] })
    );
    expect(overlay.extract?.reuseFailed).toBeUndefined();
  });

  it('gives an empty overlay without details', () => {
    expect(buildRunOverlay(undefined)).toEqual({});
    expect(buildRunOverlay()).toEqual({});
  });
});
