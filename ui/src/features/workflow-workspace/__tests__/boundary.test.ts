// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  findForbiddenStaticImports,
  MERMAID_FORBIDDEN,
} from '@/testing/importGraph';

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../..'
);

/** Every .ts and .tsx file under a folder of ui/src, at test time. */
function sourceFiles(folder: string): string[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (/\.tsx?$/.test(entry.name)) {
        files.push(path.relative(srcDir, full).split(path.sep).join('/'));
      }
    }
  };
  walk(path.join(srcDir, folder));
  return files.sort();
}

const WORKSPACE_DIR = 'features/workflow-workspace';
const POPOUT_DIR = `${WORKSPACE_DIR}/popout`;

/** The page that mounts the workspace and the pop-out logs. */
const PAGE_ROOT = 'pages/dags/dag/index.tsx';

// Built from parts, so this file does not hold the marker itself.
const STUB_MARKER = ['WORKSPACE', 'STUB'].join('_');

/** The shared leaves that the workspace mounts. */
const SHARED_LEAVES = [
  'features/dags/components/dag-editor/DAGSpec.tsx',
  'features/visual-editor/VisualEditor.tsx',
  'features/visual-editor/DetailsCanvas.tsx',
  'features/dags/components/common/DAGActions.tsx',
  'features/dag-runs/components/common/DAGRunActions.tsx',
  'features/dags/components/dag-execution/ParallelExecutionModal.tsx',
  'features/dags/components/dag-execution/StepLog.tsx',
  'features/dags/components/dag-execution/LogViewer.tsx',
  'features/dags/components/dag-execution/ExecutionLog.tsx',
  'features/dags/components/dag-execution/ForeachBodyStepLog.tsx',
  'features/dags/components/dag-execution/StatusUpdateModal.tsx',
  'features/dags/components/dag-execution/StartDAGModal.tsx',
  'features/dags/components/common/SubRunStackModal.tsx',
  'features/dags/components/step-details/StepDetailsDrawer.tsx',
  'features/dags/components/artifacts/ArtifactsTab.tsx',
  'features/dags/components/human-task/HumanTasksTab.tsx',
  'features/dags/components/approval/ApprovalTab.tsx',
  'features/dags/components/agent-session/AgentSessionTab.tsx',
  'features/dags/components/agent/AgentTimeline.tsx',
  'features/dags/components/chat-history/StepMessagesTable.tsx',
  'features/dags/components/visualization/TimelineChart.tsx',
  'features/dags/components/dag-execution/HistoryTable.tsx',
  'features/dag-runs/components/common/RunMetaLine.tsx',
  'features/dag-runs/components/common/AutoRetryBadge.tsx',
  'features/dags/components/dag-details/DAGStepTable.tsx',
  'features/dags/components/dag-details/NotificationsTab.tsx',
  'features/dags/components/dag-details/IncidentsTab.tsx',
  'features/dags/components/dag-details/WebhookTab.tsx',
  'features/dags/components/dag-details/DAGSettingsTab.tsx',
  'features/dags/components/dag-details/DAGWikiTab.tsx',
  'features/dags/components/dag-editor/DAGSpecReadOnly.tsx',
];

// parity:dup-0 parity:test-mermaid-graph - the workspace never loads Mermaid.
// There is no exception list: a hit is a static chain to remove.
describe('workflow workspace import boundary', () => {
  it('has every shared leaf on disk', () => {
    const missing = SHARED_LEAVES.filter(
      (file) => !fs.existsSync(path.join(srcDir, file))
    );
    expect(missing).toEqual([]);
  });

  // The scan parses the whole reachable tree; under a parallel run it can
  // take longer than the default 5 s.
  it('reaches no Mermaid module through a static import', () => {
    const popout = sourceFiles(POPOUT_DIR);
    expect(popout).not.toEqual([]);
    const roots = [
      ...new Set([
        PAGE_ROOT,
        ...popout,
        ...sourceFiles(WORKSPACE_DIR),
        ...SHARED_LEAVES,
      ]),
    ];
    expect(fs.existsSync(path.join(srcDir, PAGE_ROOT))).toBe(true);
    expect(findForbiddenStaticImports(roots, MERMAID_FORBIDDEN)).toEqual([]);
  }, 30_000);

  it('has no stub marker left in the workspace folder', () => {
    const stubs = sourceFiles(WORKSPACE_DIR).filter((file) =>
      fs.readFileSync(path.join(srcDir, file), 'utf8').includes(STUB_MARKER)
    );
    expect(stubs).toEqual([]);
  });
});
