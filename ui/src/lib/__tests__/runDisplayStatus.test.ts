// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import {
  continuousRunCount,
  hasContinuousStep,
  isContinuousStep,
  runDisplayStatus,
  summaryDisplayStatus,
} from '../runDisplayStatus';

const forever = { repeatPolicy: { repeat: 'while', interval: 10 } };
const limited = { repeatPolicy: { repeat: 'while', interval: 10, limit: 5 } };
const zeroLimit = { repeatPolicy: { repeat: 'until', limit: 0 } };

function node(status: NodeStatus, step: object = {}, doneCount = 0) {
  return { status, step, doneCount };
}

describe('isContinuousStep', () => {
  it.each([
    ['a repeat with no limit', forever, true],
    ['an until repeat with a limit of 0', zeroLimit, true],
    ['a repeat with a limit', limited, false],
    ['a step that does not repeat', {}, false],
  ])('%s', (_, step, expected) => {
    expect(isContinuousStep(step)).toBe(expected);
  });

  it('looks through a definition', () => {
    expect(hasContinuousStep([{}, forever])).toBe(true);
    expect(hasContinuousStep([{}, limited])).toBe(false);
    expect(hasContinuousStep(undefined)).toBe(false);
  });
});

// The one place that decides what a run is called.
describe('runDisplayStatus', () => {
  const service = { service: true };

  it('reads Healthy for a running run of a service, with no continuous step', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Running)],
        },
        service
      )
    ).toMatchObject({ healthy: true, text: 'Healthy', tone: 'success' });
  });

  it('keeps Running for an ordinary running run that is not a service', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Running)],
        },
        { service: false }
      )
    ).toMatchObject({ healthy: false, text: 'Running', tone: 'running' });
  });

  it('reads Healthy for a running run with a running continuous step', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Success), node(NodeStatus.Running, forever)],
        },
        service
      )
    ).toEqual({
      status: Status.Running,
      healthy: true,
      text: 'Healthy',
      tone: 'success',
    });
  });

  it('reads Healthy for a repeat that has a limit, as for any step of a service', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Running, limited)],
        },
        service
      ).healthy
    ).toBe(true);
  });

  it('shows the failure when a node of the run failed', () => {
    const display = runDisplayStatus(
      {
        status: Status.Running,
        nodes: [node(NodeStatus.Running, forever), node(NodeStatus.Failed)],
      },
      service
    );
    expect(display).toMatchObject({ healthy: false, text: 'Running' });
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Running, forever), node(NodeStatus.Rejected)],
        },
        service
      ).healthy
    ).toBe(false);
  });

  it('uses the normal status when the continuous step stopped', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Running,
          nodes: [node(NodeStatus.Success, forever), node(NodeStatus.Running)],
        },
        service
      ).healthy
    ).toBe(false);
    expect(
      runDisplayStatus(
        {
          status: Status.Failed,
          nodes: [node(NodeStatus.Failed, forever)],
        },
        service
      )
    ).toMatchObject({ healthy: false, text: 'Failed', tone: 'error' });
  });

  it('is Healthy only while the run is Running', () => {
    expect(
      runDisplayStatus(
        {
          status: Status.Success,
          nodes: [node(NodeStatus.Running, forever)],
        },
        service
      ).healthy
    ).toBe(false);
  });

  it('reads the normal status when the workflow is not a service DAG', () => {
    const run = {
      status: Status.Running,
      nodes: [node(NodeStatus.Running, forever)],
    };
    expect(runDisplayStatus(run, { service: false })).toEqual({
      status: Status.Running,
      healthy: false,
      text: 'Running',
      tone: 'running',
    });
    expect(runDisplayStatus(run, service).healthy).toBe(true);
  });

  it('copes with a run that has no nodes', () => {
    expect(runDisplayStatus({ status: Status.Queued }, service).text).toBe(
      'Queued'
    );
  });
});

describe('summaryDisplayStatus', () => {
  it('lets the definition decide for a run known by its summary', () => {
    const svc = { service: true };
    expect(
      summaryDisplayStatus(Status.Running, { ...svc, continuous: true }).healthy
    ).toBe(true);
    expect(
      summaryDisplayStatus(Status.Running, { ...svc, continuous: false }).text
    ).toBe('Running');
    expect(
      summaryDisplayStatus(Status.Failed, { ...svc, continuous: true }).text
    ).toBe('Failed');
    // A workflow that is not a service DAG never reads Healthy.
    expect(
      summaryDisplayStatus(Status.Running, { service: false, continuous: true })
        .text
    ).toBe('Running');
  });
});

describe('continuousRunCount', () => {
  it('is the repeat count of the continuous step', () => {
    expect(
      continuousRunCount({
        status: Status.Running,
        nodes: [node(NodeStatus.Running, forever, 7)],
      })
    ).toBe(7);
  });

  it('is null before the first run of the step', () => {
    expect(
      continuousRunCount({
        status: Status.Running,
        nodes: [node(NodeStatus.Running, forever, 0)],
      })
    ).toBeNull();
  });

  // The running step is the one that makes the run healthy.
  it('is the count of the running step when another one stopped', () => {
    expect(
      continuousRunCount({
        status: Status.Running,
        nodes: [
          node(NodeStatus.Aborted, forever, 2),
          node(NodeStatus.Running, forever, 7),
        ],
      })
    ).toBe(7);
  });
});
