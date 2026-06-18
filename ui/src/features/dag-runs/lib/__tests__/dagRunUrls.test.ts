// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { buildDAGPageURL, buildDAGRunPageURL } from '../dagRunUrls';

describe('dagRunUrls', () => {
  it('keeps remote scope in root DAG-run URLs', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'root dag',
        rootDAGRunId: 'run 1',
        remoteNode: 'edge-a',
      })
    ).toBe('/dag-runs/root%20dag/run%201?remoteNode=edge-a');
  });

  it('keeps the sub-DAG query tuple in DAG-run URLs', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        remoteNode: 'edge-a',
        subDAGRunId: 'child-run',
      })
    ).toBe(
      '/dag-runs/root-dag/root-run?remoteNode=edge-a&subDAGRunId=child-run&dagRunId=root-run&dagRunName=root-dag'
    );
  });

  it('keeps remote scope and sub-DAG context in DAG step log URLs', () => {
    expect(
      buildDAGPageURL({
        fileName: 'workflow.yaml',
        tab: 'log',
        remoteNode: 'edge-a',
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        subDAGRunId: 'child-run',
        step: 'build',
      })
    ).toBe(
      '/dags/workflow.yaml/log?remoteNode=edge-a&dagRunId=root-run&dagRunName=root-dag&subDAGRunId=child-run&step=build'
    );
  });
});
