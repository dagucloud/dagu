// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  legacyToWorkspaceUrl,
  resolveLegacyLink,
  type LegacyResult,
} from '../legacyLinks';

type Row = {
  rule: string;
  /** Path below /dags/flow.yaml, with its query. */
  url: string;
  dagName?: string | null;
  expected:
    | { kind: 'none' }
    | { kind: 'needsHistory'; idx: number }
    | {
        kind: 'redirect' | 'runPage';
        path: string;
        /** Query of the result, without remoteNode and workspace. */
        query: Record<string, string>;
      };
};

const FILE = 'flow.yaml';

// parity:overview-legacy-run-redirect
// parity:shell-run-redirect
// parity:shell-docs-redirect
// parity:page-legacy-docs-redirect
// parity:shell-subdag-run-load
// parity:actions-retry
const ROWS: Row[] = [
  {
    rule: 'R1',
    url: '/status?dagRunId=X',
    expected: { kind: 'runPage', path: '/dags/flow.yaml', query: { run: 'X' } },
  },
  // The root run opens with the sub-run on top; the step is of the sub-run.
  {
    rule: 'R1',
    url: '?dagRunId=X&dagRunName=root&subDAGRunId=Y&step=s',
    expected: {
      kind: 'runPage',
      path: '/dags/root',
      query: { run: 'X', subRun: 'Y', step: 's' },
    },
  },
  {
    rule: 'R2',
    url: '/docs?step=s',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { step: 's', panel: 'workflow', wtab: 'wiki' },
    },
  },
  {
    rule: 'R3',
    url: '/status',
    expected: { kind: 'redirect', path: '/dags/flow.yaml', query: {} },
  },
  {
    rule: 'R4',
    url: '/spec?dagRunId=X&subDAGRunId=Y',
    expected: {
      kind: 'runPage',
      path: '/dags/flow.yaml',
      query: { run: 'X', subRun: 'Y' },
    },
  },
  {
    rule: 'R5',
    url: '/spec?dagRunId=X&dagRunName=flow',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X', yaml: '1' },
    },
  },
  {
    rule: 'R5',
    url: '/spec?dagRunId=X',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X', yaml: '1' },
    },
  },
  {
    rule: 'R5',
    url: '/spec',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { yaml: '1' },
    },
  },
  // The old section routes open a panel of the editor.
  // The runtime profile of the old settings route is in the Overview tab.
  {
    rule: 'R6',
    url: '/settings',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { panel: 'workflow' },
    },
  },
  {
    rule: 'R6',
    url: '/settings?dagRunId=X&step=s',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { step: 's', panel: 'workflow' },
    },
  },
  {
    rule: 'R6',
    url: '/wiki',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { panel: 'workflow', wtab: 'wiki' },
    },
  },
  {
    rule: 'R6',
    url: '/incidents?step=s',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { step: 's', panel: 'workflow', wtab: 'incidents' },
    },
  },
  {
    rule: 'R6',
    url: '/notifications?dagRunId=X',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { panel: 'workflow', wtab: 'notifications' },
    },
  },
  {
    rule: 'R6',
    url: '/webhook',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { panel: 'webhook' },
    },
  },
  {
    rule: 'R7',
    url: '/history?dagRunId=X&idx=2',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X' },
    },
  },
  {
    rule: 'R8',
    url: '/history?idx=2',
    expected: { kind: 'needsHistory', idx: 2 },
  },
  {
    rule: 'R9',
    url: '/history',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { dock: 'timeline' },
    },
  },
  // The Executions route is the workflow page: it redirects with every key.
  {
    rule: 'R11',
    url: '/executions?run=X&step=y',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X', step: 'y' },
    },
  },
  {
    rule: 'R11',
    url: '/executions?run=X&idx=2&dock=log&ndv=expanded',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X', idx: '2', dock: 'log', ndv: 'expanded' },
    },
  },
  {
    rule: 'R11',
    url: '/executions?dagRunId=X&step=y',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { run: 'X', step: 'y' },
    },
  },
  {
    rule: 'R11',
    url: '/executions?idx=2',
    expected: { kind: 'needsHistory', idx: 2 },
  },
  {
    rule: 'R11',
    url: '/executions',
    expected: { kind: 'redirect', path: '/dags/flow.yaml', query: {} },
  },
  {
    rule: 'R10',
    url: '/log?dagRunId=X&step=s',
    expected: { kind: 'none' },
  },
  // The pop-out logs read the legacy run keys themselves.
  {
    rule: 'R10',
    url: '/log?dagRunId=X&dagRunName=root&subDAGRunId=Y&step=s&stream=stderr',
    expected: { kind: 'none' },
  },
  {
    rule: 'R10',
    url: '/dagRun-log?dagRunId=X',
    expected: { kind: 'none' },
  },
  {
    rule: 'R10',
    url: '/dagRun-log?dagRunId=X&dagRunName=root&subDAGRunId=Y',
    expected: { kind: 'none' },
  },
  {
    rule: 'R11',
    url: '/unknown-tab?step=s',
    expected: {
      kind: 'redirect',
      path: '/dags/flow.yaml',
      query: { step: 's' },
    },
  },
];

function split(url: string): [string | undefined, URLSearchParams] {
  const [path = '', query = ''] = url.split('?');
  return [path ? path.slice(1) : undefined, new URLSearchParams(query)];
}

function run(row: Row, carry: Record<string, string>): LegacyResult {
  const [tab, search] = split(row.url);
  for (const [key, value] of Object.entries(carry)) search.set(key, value);
  return legacyToWorkspaceUrl(FILE, tab, search, {
    dagName: row.dagName === undefined ? 'flow' : row.dagName,
  });
}

function sorted(params: URLSearchParams): [string, string][] {
  return [...params.entries()].sort(([a], [b]) => a.localeCompare(b));
}

const CARRIES: [string, Record<string, string>][] = [
  ['with remoteNode and workspace', { remoteNode: 'edge', workspace: 'ops' }],
];

describe('legacyToWorkspaceUrl', () => {
  describe.each(CARRIES)('%s', (_, carry) => {
    it.each(ROWS.map((row) => [row.rule, row.url, row] as const))(
      '%s %s',
      (_rule, _url, row) => {
        const result = run(row, carry);
        const { expected } = row;
        if (expected.kind === 'none' || expected.kind === 'needsHistory') {
          expect(result).toEqual(expected);
          return;
        }
        expect(result.kind).toBe(expected.kind);
        const to = new URL((result as { to: string }).to, 'http://localhost');
        expect(to.pathname).toBe(expected.path);
        const wanted = new URLSearchParams(expected.query);
        for (const [key, value] of Object.entries(carry)) {
          wanted.set(key, value);
        }
        expect(sorted(to.searchParams)).toEqual(sorted(wanted));
      }
    );
  });

  it('gives the run page a local node when the URL names none', () => {
    const result = legacyToWorkspaceUrl(
      FILE,
      undefined,
      new URLSearchParams('dagRunId=X'),
      { dagName: 'flow' }
    );

    expect(result).toEqual({
      kind: 'runPage',
      to: '/dags/flow.yaml?remoteNode=local&run=X',
    });
  });

  // The ?ui switch is gone: a redirect that builds its query drops it.
  it.each(['classic', 'workspace', 'modern'])(
    'does not carry ?ui=%s to a built query',
    (ui) => {
      const result = legacyToWorkspaceUrl(
        FILE,
        'history',
        new URLSearchParams(`ui=${ui}`),
        { dagName: 'flow' }
      );

      expect(result).toEqual({
        kind: 'redirect',
        to: '/dags/flow.yaml?dock=timeline',
      });
    }
  );

  it.each([
    ['?dagRunId=X', undefined],
    ['?dagRunId=X', 'status'],
    ['?dagRunId=X&subDAGRunId=Y', 'spec'],
  ] as const)('waits for the workflow name on %s (tab %s)', (query, tab) => {
    const search = new URLSearchParams(query);

    expect(resolveLegacyLink(FILE, tab, search, { dagName: null })).toEqual({
      kind: 'waitDagName',
    });
    expect(legacyToWorkspaceUrl(FILE, tab, search, { dagName: null })).toEqual({
      kind: 'none',
    });
  });

  // The run page stays on the workflow page; /executions redirects to it
  // with replace and keeps every key.
  // parity:chg-classic-executions-link parity:chg-single-tab
  // parity:embed-workflow-history
  it.each([
    ['run=X&step=s', '/dags/flow.yaml?run=X&step=s'],
    ['view=grid', '/dags/flow.yaml?view=grid'],
    [
      'run=X&remoteNode=edge&workspace=ops',
      '/dags/flow.yaml?run=X&remoteNode=edge&workspace=ops',
    ],
  ] as const)('redirects /executions?%s to %s', (query, to) => {
    expect(
      legacyToWorkspaceUrl(FILE, 'executions', new URLSearchParams(query), {
        dagName: 'flow',
      })
    ).toEqual({ kind: 'redirect', to });
  });

  it('keeps ?run on the workflow route', () => {
    expect(
      legacyToWorkspaceUrl(
        FILE,
        undefined,
        new URLSearchParams('run=X&step=s'),
        {
          dagName: 'flow',
        }
      )
    ).toEqual({ kind: 'none' });
  });

  it('uses dagRunName without the workflow name', () => {
    const result = resolveLegacyLink(
      FILE,
      undefined,
      new URLSearchParams('dagRunId=X&dagRunName=root'),
      { dagName: null }
    );

    expect(result).toEqual({
      kind: 'runPage',
      to: '/dags/root?remoteNode=local&run=X',
    });
  });

  it('treats a history idx that is not a number as no idx', () => {
    const result = legacyToWorkspaceUrl(
      FILE,
      'history',
      new URLSearchParams('idx=last'),
      { dagName: 'flow' }
    );

    expect(result).toEqual({
      kind: 'redirect',
      to: '/dags/flow.yaml?dock=timeline',
    });
  });

  it('keeps remoteNode and workspace on R7 and R9', () => {
    const carry = 'remoteNode=edge&workspace=ops&ui=workspace';
    expect(
      legacyToWorkspaceUrl(
        FILE,
        'history',
        new URLSearchParams(`dagRunId=X&${carry}`),
        { dagName: 'flow' }
      )
    ).toEqual({
      kind: 'redirect',
      to: '/dags/flow.yaml?run=X&remoteNode=edge&workspace=ops',
    });
    expect(
      legacyToWorkspaceUrl(FILE, 'history', new URLSearchParams(carry), {
        dagName: 'flow',
      })
    ).toEqual({
      kind: 'redirect',
      to: '/dags/flow.yaml?dock=timeline&remoteNode=edge&workspace=ops',
    });
  });
});
