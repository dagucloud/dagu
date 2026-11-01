// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  buildDAGPageURL,
  buildDAGRunPageURL,
  buildRunningTaskURL,
} from '../dagRunUrls';

// parity:common-url-builders
describe('dagRunUrls', () => {
  it('opens a root DAG-run on its workflow page', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'root dag',
        rootDAGRunId: 'run 1',
        remoteNode: 'edge-a',
      })
    ).toBe('/dags/root%20dag?remoteNode=edge-a&run=run+1');
  });

  // The name of a DAG can differ from the name of its file.
  it('opens the workflow file of the root run when the caller has it', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'etl',
        rootDAGRunId: 'r1',
        fileName: 'etl_file',
        remoteNode: 'local',
      })
    ).toBe('/dags/etl_file?remoteNode=local&run=r1');
  });

  // The step of a sub-run link is a step of the sub-run.
  it('opens a sub-DAG run on the page of its root run with ?subRun', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        remoteNode: 'edge-a',
        subDAGRunId: 'child-run',
        step: 'build',
      })
    ).toBe(
      '/dags/root-dag?remoteNode=edge-a&run=root-run&subRun=child-run&step=build'
    );
  });

  // A parent link passes the parent as the sub-run, which can be the root.
  it('opens the root run when the sub-run is the root run', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        remoteNode: 'local',
        subDAGRunId: 'root-run',
      })
    ).toBe('/dags/root-dag?remoteNode=local&run=root-run');
  });

  it('keeps the workspace, step and dock tab of a run URL', () => {
    expect(
      buildDAGRunPageURL({
        rootDAGRunName: 'flow',
        rootDAGRunId: 'r1',
        remoteNode: 'local',
        workspace: 'ops',
        step: 'build',
        dock: 'artifacts',
      })
    ).toBe(
      '/dags/flow?remoteNode=local&workspace=ops&run=r1&step=build&dock=artifacts'
    );
  });

  it('builds the run URL of a worker task from its root run', () => {
    expect(
      buildRunningTaskURL({ dagName: 'solo', dagRunId: 'r1' }, 'local')
    ).toBe('/dags/solo?remoteNode=local&run=r1');
    expect(
      buildRunningTaskURL(
        {
          dagName: 'child',
          dagRunId: 'c1',
          parentDagRunName: 'mid',
          parentDagRunId: 'm1',
          rootDagRunName: 'top',
          rootDagRunId: 't1',
        },
        'local'
      )
    ).toBe('/dags/top?remoteNode=local&run=t1');
    expect(
      buildRunningTaskURL(
        {
          dagName: 'child',
          dagRunId: 'c1',
          parentDagRunName: 'mid',
          parentDagRunId: 'm1',
        },
        'local'
      )
    ).toBe('/dags/mid?remoteNode=local&run=m1');
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

  // Every call shape that existed before the workspace options keeps its output.
  it.each([
    [
      { fileName: 'flow.yaml', remoteNode: 'local' },
      '/dags/flow.yaml?remoteNode=local',
    ],
    [
      { fileName: 'my flow', remoteNode: 'edge', tab: 'spec' },
      '/dags/my%20flow/spec?remoteNode=edge',
    ],
    [
      {
        fileName: 'flow.yaml',
        remoteNode: 'local',
        tab: 'log',
        rootDAGRunId: 'r1',
        step: 'build',
        stream: 'stderr',
      },
      '/dags/flow.yaml/log?remoteNode=local&dagRunId=r1&step=build&stream=stderr',
    ],
    [
      { fileName: 'flow.yaml', remoteNode: 'local', step: 'build' },
      '/dags/flow.yaml?remoteNode=local&step=build',
    ],
  ] as const)('keeps the output of %o', (input, url) => {
    expect(buildDAGPageURL(input)).toBe(url);
  });

  it('selects a run with ?run in the workspace form', () => {
    expect(
      buildDAGPageURL({
        fileName: 'flow.yaml',
        remoteNode: 'edge',
        run: 'run 1',
        step: 'build',
        dock: 'log',
      })
    ).toBe('/dags/flow.yaml?remoteNode=edge&run=run+1&step=build&dock=log');
  });

  it('has no mode: the run goes to the workflow route', () => {
    expect(
      buildDAGPageURL({
        fileName: 'my flow',
        remoteNode: 'local',
        run: 'r1',
      })
    ).toBe('/dags/my%20flow?remoteNode=local&run=r1');
  });

  it('never writes dagRunId in the workspace form', () => {
    const url = buildDAGPageURL({
      fileName: 'flow.yaml',
      remoteNode: 'local',
      run: 'r1',
      dock: 'log',
    });
    expect(url).toBe('/dags/flow.yaml?remoteNode=local&run=r1&dock=log');
    expect(new URL(url, 'http://x').searchParams.has('dagRunId')).toBe(false);
  });
});
