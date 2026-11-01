// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';

export type LegacyResult =
  | { kind: 'none' }
  /** navigate(to, { replace: true }) */
  | { kind: 'redirect'; to: string }
  /** The workflow page of the root run (buildDAGRunPageURL), replace */
  | { kind: 'runPage'; to: string }
  /** Workspace only; resolved at the root of the workspace. */
  | { kind: 'needsHistory'; idx: number };

/** A run page rule applies, but its root name is not known yet. */
export type LegacyResolution = LegacyResult | { kind: 'waitDagName' };

/**
 * Old section routes that are panels now: the Workflow panel on a tab, or the
 * Webhook launcher. The value is the query that opens the panel. The old
 * settings route held the runtime profile, which is in the Overview tab.
 */
const PANEL_TABS: ReadonlyMap<string, readonly [string, string][]> = new Map([
  [
    'wiki',
    [
      ['panel', 'workflow'],
      ['wtab', 'wiki'],
    ],
  ],
  [
    'incidents',
    [
      ['panel', 'workflow'],
      ['wtab', 'incidents'],
    ],
  ],
  [
    'notifications',
    [
      ['panel', 'workflow'],
      ['wtab', 'notifications'],
    ],
  ],
  ['webhook', [['panel', 'webhook']]],
  ['settings', [['panel', 'workflow']]],
]);

/** The query of a panel link: the legacy run keys go, as a panel is not about one run. */
function panelQuery(
  search: URLSearchParams,
  entries: readonly [string, string][]
): URLSearchParams {
  const query = new URLSearchParams(search);
  for (const key of ['dagRunId', 'dagRunName', 'subDAGRunId']) {
    query.delete(key);
  }
  for (const [key, value] of entries) query.set(key, value);
  return query;
}

function text(search: URLSearchParams, key: string): string | null {
  const value = search.get(key);
  return value ? value : null;
}

/** The query keys that every redirect keeps: remoteNode and workspace. */
function carried(search: URLSearchParams): URLSearchParams {
  const query = new URLSearchParams();
  const remoteNode = text(search, 'remoteNode');
  if (remoteNode !== null) query.set('remoteNode', remoteNode);
  const workspace = text(search, 'workspace');
  if (workspace !== null) query.set('workspace', workspace);
  return query;
}

function join(path: string, query: URLSearchParams): string {
  const q = query.toString();
  return q ? `${path}?${q}` : path;
}

function dagPath(fileName: string, tab?: string): string {
  const base = `/dags/${encodeURIComponent(fileName)}`;
  return tab ? `${base}/${tab}` : base;
}

function redirect(to: string): LegacyResult {
  return { kind: 'redirect', to };
}

/** The legacy run query (dagRunId, dagRunName, subDAGRunId) becomes ?run. */
function withRun(search: URLSearchParams): URLSearchParams {
  const query = new URLSearchParams(search);
  const dagRunId = text(search, 'dagRunId');
  query.delete('dagRunId');
  query.delete('dagRunName');
  query.delete('subDAGRunId');
  if (dagRunId !== null) query.set('run', dagRunId);
  return query;
}

function runPage(
  fileName: string,
  search: URLSearchParams,
  rootName: string | null,
  runId: string,
  subDAGRunId: string | null
): LegacyResolution {
  if (!rootName) return { kind: 'waitDagName' };
  // Without dagRunName the root run is a run of this workflow, whose file
  // name may differ from its DAG name.
  const url = buildDAGRunPageURL({
    rootDAGRunName: text(search, 'dagRunName') ?? fileName,
    rootDAGRunId: runId,
    remoteNode: text(search, 'remoteNode') ?? 'local',
    subDAGRunId: subDAGRunId ?? undefined,
    step: text(search, 'step') ?? undefined,
  });
  const extra = carried(search);
  extra.delete('remoteNode');
  const q = extra.toString();
  if (!q) return { kind: 'runPage', to: url };
  return { kind: 'runPage', to: `${url}${url.includes('?') ? '&' : '?'}${q}` };
}

/**
 * Like legacyToWorkspaceUrl, but tells when a run page rule waits for the
 * workflow name (no dagRunName in the query and no dagName yet).
 */
export function resolveLegacyLink(
  fileName: string,
  tab: string | undefined,
  search: URLSearchParams,
  ctx: { dagName: string | null }
): LegacyResolution {
  const dagRunId = text(search, 'dagRunId');
  const subDAGRunId = text(search, 'subDAGRunId');
  const rootName = text(search, 'dagRunName') ?? ctx.dagName;

  // R1
  if ((tab === undefined || tab === 'status') && dagRunId !== null) {
    return runPage(fileName, search, rootName, dagRunId, subDAGRunId);
  }
  // R2
  if (tab === 'docs') {
    return redirect(
      join(dagPath(fileName), panelQuery(search, PANEL_TABS.get('wiki')!))
    );
  }

  if (tab === undefined) return { kind: 'none' };
  // R3
  if (tab === 'status') return redirect(join(dagPath(fileName), search));
  if (tab === 'spec') {
    // R4
    if (subDAGRunId !== null && dagRunId !== null) {
      return runPage(fileName, search, rootName, dagRunId, subDAGRunId);
    }
    // R5: the old Spec tab was the YAML, so the editor opens it (?yaml=1).
    const query = withRun(search);
    query.set('yaml', '1');
    return redirect(join(dagPath(fileName), query));
  }
  const panel = PANEL_TABS.get(tab);
  if (panel) {
    return redirect(join(dagPath(fileName), panelQuery(search, panel)));
  }
  if (tab === 'history') {
    const query = carried(search);
    // R7: dagRunId wins over idx; the run opens on the History tab, as R9.
    if (dagRunId !== null) {
      const runQuery = new URLSearchParams([
        ['run', dagRunId],
        ['dock', 'history'],
      ]);
      for (const [key, value] of query) runQuery.append(key, value);
      return redirect(join(dagPath(fileName), runQuery));
    }
    // R8
    const idx = search.get('idx');
    if (idx !== null && /^\d+$/.test(idx)) {
      return { kind: 'needsHistory', idx: Number(idx) };
    }
    // R9: the History grid is the History tab of the Run panel.
    const historyQuery = new URLSearchParams([['dock', 'history']]);
    for (const [key, value] of query) historyQuery.append(key, value);
    return redirect(join(dagPath(fileName), historyQuery));
  }
  // R10
  if (tab === 'log' || tab === 'dagRun-log') {
    return { kind: 'none' };
  }
  // R11 for /executions: the old Executions route is the workflow page, and
  // ?run and every other key stay. The two legacy history keys of the old
  // route still resolve: dagRunId becomes ?run, and a bare idx needs the history.
  if (tab === 'executions' && search.get('run') === null) {
    if (dagRunId !== null)
      return redirect(join(dagPath(fileName), withRun(search)));
    const idx = search.get('idx');
    if (idx !== null && /^\d+$/.test(idx)) {
      return { kind: 'needsHistory', idx: Number(idx) };
    }
  }
  // R11
  return redirect(join(dagPath(fileName), search));
}

/** Pure. Every redirect keeps remoteNode and workspace. rootName = dagRunName ?? dagName. */
export function legacyToWorkspaceUrl(
  fileName: string,
  tab: string | undefined,
  search: URLSearchParams,
  ctx: { dagName: string | null }
): LegacyResult {
  const result = resolveLegacyLink(fileName, tab, search, ctx);
  return result.kind === 'waitDagName' ? { kind: 'none' } : result;
}
