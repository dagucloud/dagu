// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The workspace takes its status colours from lib/statusPalette.ts and its
// durations from lib/dagRunTiming.ts. This scan keeps other colours, spinners
// and formatters out of features/workflow-workspace. It also scans the editor
// code that the workspace hosts: there, a frozen baseline lists the literals
// that exist today. A count can go down, never up.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

export type TokenRule =
  | 'hex-colour'
  | 'rgb-hsl-colour'
  | 'tailwind-palette'
  | 'animate-spin'
  | 'formatter-import';

export interface TokenViolation {
  /** Path relative to ui/src. */
  file: string;
  rule: TokenRule;
}

/**
 * Violations in files that W5-N7 or W5-N6C own. Each task removes its
 * entries; W6-Q1 empties the list.
 */
export const PENDING_W5: readonly TokenViolation[] = [];

/**
 * Literals that stay for a reason, by file, with their count. A file here
 * can go down to a lower count (lower the number) and never up.
 * exportCanvas.ts draws the PNG and SVG export outside the page, where CSS
 * variables do not resolve, so its palette is literal.
 */
export const BASELINE: Readonly<Record<string, number>> = {
  'features/visual-editor/canvas/exportCanvas.ts': 26,
};

/** The one place each of these literals may appear. */
const ANIMATE_SPIN_FILES = [
  'components/ui/spinner.tsx',
  'components/ui/loading-indicator.tsx',
];

/** The directories that the scan reads. The first is strict: no baseline. */
const SCAN_DIRS = [
  'features/workflow-workspace',
  'features/visual-editor',
  'features/dags/components/dag-editor',
  'features/dags/components/step-details',
];

/** The workspace files that T28 owns: none of them can be pending. */
const OWNED = [
  'features/workflow-workspace/bar/RunChip.tsx',
  'features/workflow-workspace/bar/RunDetailsParts.tsx',
  'features/workflow-workspace/bar/RunDetailsPopover.tsx',
  'features/workflow-workspace/dock/RunStepList.tsx',
  'features/workflow-workspace/executions/RunStatusDot.tsx',
  'features/workflow-workspace/dock/RunTimeline.tsx',
  'features/workflow-workspace/messages/t28.ts',
  'features/workflow-workspace/step/StepFacts.tsx',
];

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../..'
);

const PALETTE =
  'red|green|blue|yellow|amber|pink|purple|violet|emerald|orange|rose|sky|indigo|lime|teal|cyan|fuchsia|slate|gray|zinc|neutral|stone';
const PATTERNS: Array<[TokenRule, RegExp]> = [
  [
    'hex-colour',
    /(?<![\w&])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])/,
  ],
  ['rgb-hsl-colour', /\b(?:rgba?|hsla?)\(/],
  [
    'tailwind-palette',
    new RegExp(
      `\\b(?:bg|text|border|ring|stroke|fill|from|via|to|shadow|outline|divide|decoration|accent|caret)-(?:${PALETTE})-\\d{2,3}\\b`
    ),
  ],
  ['animate-spin', /\banimate-spin\b/],
];

/** Formatters that the workspace must not import, by name. */
const BANNED_NAMES = [
  'formatDurationMs',
  'formatRoughDurationMs',
  'formatStepTime',
  'getStatusColors',
];

function bannedImport(source: string): boolean {
  const statement =
    /\b(?:import|export)\s+(?:type\s+)?([\s\S]*?)\s+from\s+['"]([^'"]+)['"]/g;
  for (const [, clause = '', from = ''] of source.matchAll(statement)) {
    const names = (clause.match(/\{([\s\S]*)\}/)?.[1] ?? '').split(',').map(
      (part) =>
        part
          .trim()
          .replace(/^type\s+/, '')
          .split(/\s+as\s+/)[0]
    );
    if (names.some((name) => BANNED_NAMES.includes(name ?? ''))) return true;
    if (/(^|\/)formatDuration$/.test(from) && clause.includes('*')) return true;
  }
  return false;
}

/** The rules that a source text breaks. */
export function tokenRules(source: string): TokenRule[] {
  const rules: TokenRule[] = PATTERNS.filter(([, re]) => re.test(source)).map(
    ([rule]) => rule
  );
  if (bannedImport(source)) rules.push('formatter-import');
  return rules;
}

/** The number of literals and banned imports in a source text. */
export function literalCount(source: string): number {
  const literals = PATTERNS.reduce(
    (sum, [, re]) =>
      sum + (source.match(new RegExp(re.source, 'g'))?.length ?? 0),
    0
  );
  return literals + (bannedImport(source) ? 1 : 0);
}

/** Every non-test .ts and .tsx file under the scanned directories. */
function scanFiles(dirs: readonly string[] = SCAN_DIRS): string[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== '__tests__') walk(full);
      } else if (
        /\.tsx?$/.test(entry.name) &&
        !/\.test\.tsx?$/.test(entry.name)
      ) {
        files.push(path.relative(srcDir, full).split(path.sep).join('/'));
      }
    }
  };
  for (const dir of dirs) walk(path.join(srcDir, dir));
  return files.sort();
}

const read = (file: string) => fs.readFileSync(path.join(srcDir, file), 'utf8');

function scan(): TokenViolation[] {
  return scanFiles().flatMap((file) =>
    tokenRules(read(file))
      .filter(
        (rule) => rule !== 'animate-spin' || !ANIMATE_SPIN_FILES.includes(file)
      )
      .map((rule) => ({ file, rule }))
  );
}

const key = (v: TokenViolation) => `${v.file} ${v.rule}`;
const pendingFiles = () => new Set(PENDING_W5.map((v) => v.file));

// parity:dup-23
describe('workspace status tokens', () => {
  it('finds the five run surfaces in the scan', () => {
    const files = scanFiles();
    for (const file of OWNED.filter((f) => !f.endsWith('t28.ts'))) {
      expect(files).toContain(file);
    }
  });

  it('scans the editor directories that the workspace hosts', () => {
    const files = scanFiles();
    for (const file of [
      'features/visual-editor/canvas/exportCanvas.ts',
      'features/dags/components/dag-editor/DAGSpec.tsx',
      'features/dags/components/step-details/StepDetails.tsx',
    ]) {
      expect(files).toContain(file);
    }
  });

  it('finds no colour, spinner or formatter outside the shared modules', () => {
    const pending = new Set(PENDING_W5.map(key));
    const baseline = new Set(Object.keys(BASELINE));
    expect(
      scan().filter((v) => !pending.has(key(v)) && !baseline.has(v.file))
    ).toEqual([]);
  });

  it('lists only pending entries that still break their rule', () => {
    const found = new Set(scan().map(key));
    expect(PENDING_W5.filter((v) => !found.has(key(v)))).toEqual([]);
  });

  it('lists no file that T28 owns as pending', () => {
    expect(PENDING_W5.filter((v) => OWNED.includes(v.file))).toEqual([]);
  });

  // The count of a baseline file can go down. Lower the number when it does.
  it('holds each baseline file at its frozen count', () => {
    const files = scanFiles();
    const counts = Object.fromEntries(
      Object.keys(BASELINE).map((file) => [
        file,
        files.includes(file) ? literalCount(read(file)) : null,
      ])
    );
    expect(counts).toEqual(BASELINE);
  });

  it('keeps the baseline and the pending list apart', () => {
    const pending = pendingFiles();
    expect(Object.keys(BASELINE).filter((file) => pending.has(file))).toEqual(
      []
    );
  });

  it('keeps animate-spin in the files that own it', () => {
    for (const file of ANIMATE_SPIN_FILES) {
      expect(read(file)).toContain('animate-spin');
    }
  });
});

// Two exported formatters with one name in two modules make two near
// pairs: the callers pick one at random and the texts drift apart.
describe('formatter names', () => {
  const FORMATTER_FILES = [
    'lib/dagRunTiming.ts',
    'features/visual-editor/canvas/statusStyle.ts',
    ...fs
      .readdirSync(path.join(srcDir, 'lib'))
      .filter((name) => /^format.*\.tsx?$/.test(name))
      .map((name) => `lib/${name}`),
  ];

  it('has no exported function name in two formatter modules', () => {
    const owners = new Map<string, string[]>();
    for (const file of new Set(FORMATTER_FILES)) {
      const names = [
        ...read(file).matchAll(
          /^export\s+(?:async\s+)?(?:function|const)\s+(\w+)/gm
        ),
      ].map((m) => m[1] as string);
      for (const name of names) {
        owners.set(name, [...(owners.get(name) ?? []), file]);
      }
    }
    const shared = [...owners].filter(([, files]) => files.length > 1);
    expect(shared).toEqual([]);
  });
});

describe('tokenRules', () => {
  it.each([
    ["const c = '#c8382f';", 'hex-colour'],
    ["style={{ color: '#fff' }}", 'hex-colour'],
    ["fill: 'rgb(0 0 0)'", 'rgb-hsl-colour'],
    ["color: 'rgba(0, 0, 0, 0.5)'", 'rgb-hsl-colour'],
    ["color: 'hsl(210 40% 50%)'", 'rgb-hsl-colour'],
    ["className='bg-red-500'", 'tailwind-palette'],
    ["className='hover:text-emerald-600'", 'tailwind-palette'],
    ["className='dark:stroke-sky-50'", 'tailwind-palette'],
    [
      "import { formatDurationMs } from '@/lib/formatDuration';",
      'formatter-import',
    ],
    [
      "import {\n  formatRoughDurationMs as rough,\n} from '../../lib/formatDuration';",
      'formatter-import',
    ],
    ["import * as d from '@/lib/formatDuration';", 'formatter-import'],
    [
      "import { getStatusColors } from '@/lib/status-utils';",
      'formatter-import',
    ],
    ["import { formatStepTime } from '@/lib/formatStep';", 'formatter-import'],
    ["className='text-slate-500'", 'tailwind-palette'],
    ["className='dark:bg-zinc-900'", 'tailwind-palette'],
    ["className='border-gray-200'", 'tailwind-palette'],
    ["className='text-neutral-500'", 'tailwind-palette'],
    ["className='bg-gradient-to-r from-red-500'", 'tailwind-palette'],
    ['<Loader2 className="size-4 animate-spin" />', 'animate-spin'],
    ["className='motion-safe:animate-spin'", 'animate-spin'],
  ] as const)('flags %s', (source, rule) => {
    expect(tokenRules(source)).toEqual([rule]);
  });

  it.each([
    "className='bg-[var(--status-error)] text-destructive ring-success'",
    "className='bg-red text-amber'",
    "import { formatElapsed } from '@/lib/dagRunTiming';",
    "import { statusRingClass } from '@/features/visual-editor/canvas/statusStyle';",
    "className='text-slate-5 animate-pulse'",
    "import { getStatusClass } from '@/lib/status-utils';",
    "const anchor = '#add-step'; const issue = 'dup-23 #23';",
    'const entity = "&#123;";',
  ])('lets %s pass', (source) => {
    expect(tokenRules(source)).toEqual([]);
  });
});

describe('literalCount', () => {
  it('counts each literal and a banned import once', () => {
    expect(literalCount("const a = ['#fff', '#000000']; // rgb(0 0 0)")).toBe(
      3
    );
    expect(
      literalCount("import { formatDurationMs } from '@/lib/formatDuration';")
    ).toBe(1);
    expect(literalCount("const none = 'bg-primary';")).toBe(0);
  });
});
