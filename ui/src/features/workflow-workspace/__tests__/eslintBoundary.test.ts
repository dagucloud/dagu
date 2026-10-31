// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The default jsdom environment is kept: src/test/setup.ts patches Element,
// which the node environment does not define.

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ESLint } from 'eslint';
import { beforeAll, describe, expect, it } from 'vitest';

const uiRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../..'
);
const workspaceFile = 'src/features/workflow-workspace/__virtual__/Probe.tsx';
const outsideFile = 'src/features/dags/__virtual__/Probe.tsx';

let eslint: ESLint;

beforeAll(() => {
  eslint = new ESLint({ cwd: uiRoot });
});

// Only the import rule matters: the probe text has unused imports.
async function restrictedImportErrors(
  source: string,
  filePath = workspaceFile
): Promise<string[]> {
  const [result] = await eslint.lintText(
    `import probe from '${source}';\nexport default probe;\n`,
    { filePath: path.join(uiRoot, filePath) }
  );
  return (result?.messages ?? [])
    .filter((m) => m.ruleId === 'no-restricted-imports' && m.severity === 2)
    .map((m) => m.message);
}

const forbidden = [
  // Barrels, with no subpath.
  '@/features/dags/components',
  '@/features/dags/components/index',
  '@/features/dags/components/common',
  '@/features/dags/components/common/index',
  '@/features/dags/components/dag-execution',
  '@/features/dags/components/dag-details',
  '@/features/dags/components/visualization',
  '@/features/dags/components/step-details',
  '../../dags/components/common',
  // The run view and the Mermaid graph.
  '@/features/dags/components/DAGStatus',
  '@/features/dags/components/dag-details/DAGStatusOverview',
  '@/features/dags/components/dag-details/NodeStatusTable',
  '@/features/dags/components/dag-details/NodeStatusTableRow',
  '@/features/dags/components/dag-execution/DAGExecutionHistory',
  '@/features/dags/components/visualization/DAGGraph',
  '@/features/dags/components/visualization/Graph',
  '@/features/dags/components/step-details/StepDetailsDrawer',
  '@/features/dags/components/dag-execution/LogSideModal',
  '@/features/dags/components/dag-details/DAGDetailsContent',
  '@/features/dags/components/dag-details/DAGHeader',
  '../../dags/components/DAGStatus.tsx',
  // The component library rule stays in force.
  '@/ui/button',
  '../../components/ui/button',
  // The shared primitives replace these.
  '@/features/dags/components/common/StatusDot',
  '../../dags/components/common/StatusDot',
  '@/components/ui/reload-button',
];

const allowed = [
  '@/components/ui/status-dot',
  '@/features/dags/components/common/DAGActions',
  '@/features/dags/components/dag-details/DAGSettingsTab',
  '@/features/dags/components/dag-editor/DAGSpec',
  '@/features/dags/components/dag-execution/StepLog',
  '@/features/dags/components/step-details/StepDetails',
  '@/components/ui/button',
];

const editorFile = 'src/features/visual-editor/__virtual__/Probe.tsx';
const dagEditorFile =
  'src/features/dags/components/dag-editor/__virtual__/Probe.tsx';

// Messages of the no-restricted-syntax and no-restricted-imports rules for
// a source text. The probe text has other lint errors, which are ignored.
async function guardMessages(
  source: string,
  filePath: string
): Promise<string[]> {
  const [result] = await eslint.lintText(source, {
    filePath: path.join(uiRoot, filePath),
  });
  return (result?.messages ?? [])
    .filter(
      (m) =>
        (m.ruleId === 'no-restricted-syntax' ||
          m.ruleId === 'no-restricted-imports') &&
        m.severity === 2
    )
    .map((m) => m.message);
}

// A planted violation fails the rule in the three guarded folders and passes
// outside them.
const planted: Array<[string, string, string]> = [
  ['dayjs from', 'const a = dayjs(x).from(y);', 'RelativeTime'],
  ['dayjs fromNow', 'const a = dayjs(x).fromNow();', 'RelativeTime'],
  ['dayjs toNow', 'const a = d.toNow();', 'RelativeTime'],
  ['clipboard', 'navigator.clipboard.writeText(t);', 'copyText'],
  ['window clipboard', 'window.navigator.clipboard.readText();', 'copyText'],
  ['computed clipboard', "navigator['clipboard'].writeText(t);", 'copyText'],
  ['destructured clipboard', 'const { clipboard } = navigator;', 'copyText'],
  ['toLocaleString', 'const a = d.toLocaleString();', 'formatAbsoluteTime'],
  [
    'toLocaleDateString',
    'const a = d.toLocaleDateString();',
    'formatAbsoluteTime',
  ],
  [
    'toLocaleTimeString',
    'const a = d.toLocaleTimeString();',
    'formatAbsoluteTime',
  ],
  ['showError literal', "showError('Failed to save');", 'ts()'],
  ['showToast literal', "showToast('Saved');", 'ts()'],
  ['showToast template', 'showToast(`Saved ${n}`);', 'ts()'],
  ['showToast branch', "showToast(ok ? 'Saved' : 'Failed');", 'ts()'],
  [
    'ring literal',
    "const c = 'focus-visible:ring-primary';",
    'focus-visible:ring-ring',
  ],
  [
    'ring template',
    'const c = `focus-visible:ring-primary ${x}`;',
    'focus-visible:ring-ring',
  ],
  [
    'reload button',
    "import { ReloadButton } from '@/components/ui/reload-button';",
    'RefreshButton',
  ],
  [
    'old StatusDot',
    "import { StatusDot } from '../common/StatusDot';",
    'StatusDot',
  ],
  [
    'formatDurationMs',
    "import { formatDurationMs } from '@/lib/formatDuration';",
    'formatElapsed',
  ],
  [
    'formatStepTime',
    "import { formatStepTime } from './steps';",
    'formatElapsed',
  ],
];

const passing = [
  'const a = Array.from(set);',
  'const a = Uint8Array.from(list);',
  'const m = YAMLMap.from(schema, value);',
  'const s = YAMLSeq.from(schema, list);',
  'const i = Iterator.from(source);',
  "showError(ts('Failed to save'));",
  "showToast(ts('Saved'), { tone: 'error' });",
  "showToast(ok ? ts('Saved') : ts('Failed'));",
  'const c = `focus-visible:ring-ring ${x}`;',
  'const n = formatElapsed(a, b);',
  "import { formatElapsed } from '@/lib/dagRunTiming';",
  "import { StatusDot } from '@/components/ui/status-dot';",
];

describe('shared-module guards', { timeout: 60_000 }, () => {
  const hosts = [workspaceFile, editorFile, dagEditorFile];

  it.each(
    planted.flatMap(([name, source, text]) =>
      hosts.map((host) => [name, host, source, text] as const)
    )
  )('rejects %s in %s', async (_name, host, source, text) => {
    const messages = await guardMessages(source, host);
    expect(messages.some((message) => message.includes(text))).toBe(true);
  });

  it.each(passing)('allows %s', async (source) => {
    for (const host of hosts) {
      expect(await guardMessages(source, host)).toEqual([]);
    }
  });

  it('applies only to the guarded folders', async () => {
    expect(await guardMessages('const a = d.fromNow();', outsideFile)).toEqual(
      []
    );
  });
});

// The probes lint planted text. CI runs no UI lint step, so this block
// lints the real files of the guarded folders with the same rules.
describe('guarded folders', { timeout: 120_000 }, () => {
  it('break no guard rule', async () => {
    const results = await eslint.lintFiles([
      'src/features/workflow-workspace',
      'src/features/visual-editor',
      'src/features/dags/components/dag-editor',
    ]);
    const errors = results.flatMap((result) =>
      result.messages
        .filter(
          (m) => m.ruleId?.startsWith('no-restricted-') && m.severity === 2
        )
        .map(
          (m) =>
            `${path.relative(uiRoot, result.filePath)}:${m.line} ${m.message}`
        )
    );
    expect(results.length).toBeGreaterThan(100);
    expect(errors).toEqual([]);
  });
});

describe('workspace ESLint boundary', { timeout: 60_000 }, () => {
  it.each(forbidden)('rejects %s', async (source) => {
    expect(await restrictedImportErrors(source)).toHaveLength(1);
  });

  it.each(allowed)('allows %s', async (source) => {
    expect(await restrictedImportErrors(source)).toEqual([]);
  });

  it('applies only to the workspace folder', async () => {
    expect(
      await restrictedImportErrors('@/features/dags/components', outsideFile)
    ).toEqual([]);
    expect(
      await restrictedImportErrors('@/ui/button', outsideFile)
    ).toHaveLength(1);
  });
});
