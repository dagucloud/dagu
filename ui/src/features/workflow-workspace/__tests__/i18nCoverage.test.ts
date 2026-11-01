// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Every text that the workspace shows goes through the message catalogs.
// This gate scans the workspace JSX and the editor files that the workspace
// hosts. It has no exception list: mark a literal that is not a message
// with `// i18n-ignore` next to it.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { baseStaticMessages } from '@/i18n/staticMessages';
import { findJsxLiterals } from '@/testing/jsxLiterals';
import { workspaceMessages } from '../messages';

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../..'
);

const WORKSPACE_DIR = 'features/workflow-workspace';

/** Editor files that the workspace mounts in host mode. */
const HOSTED_FILES = [
  'features/dags/components/dag-editor/NodeDetailsFrame.tsx',
  'features/dags/components/dag-editor/IssuesPopover.tsx',
  'features/dags/components/dag-editor/NoticeLine.tsx',
  'features/visual-editor/canvas/ContextMenu.tsx',
  'features/visual-editor/canvas/KeyHelpDialog.tsx',
  'features/visual-editor/canvas/EmptyCanvasTile.tsx',
  'features/visual-editor/canvas/CanvasToolbar.tsx',
] as const;

/** The non-test .tsx files under features/workflow-workspace. */
function workspaceTsxFiles(): string[] {
  const out: string[] = [];
  const walk = (rel: string) => {
    for (const entry of fs.readdirSync(path.join(srcDir, rel), {
      withFileTypes: true,
    })) {
      const child = `${rel}/${entry.name}`;
      if (entry.isDirectory()) {
        if (entry.name !== '__tests__') walk(child);
      } else if (
        entry.name.endsWith('.tsx') &&
        !/\.(test|spec)\.tsx$/.test(entry.name)
      ) {
        out.push(child);
      }
    }
  };
  walk(WORKSPACE_DIR);
  return out.sort();
}

describe('workspace i18n coverage', () => {
  // parity:crit-i18n-locales
  it('leaves no untranslated literal in the workspace JSX', () => {
    const files = [...workspaceTsxFiles(), ...HOSTED_FILES];
    const hits = findJsxLiterals(files, { srcDir }).map(
      (hit) => `${hit.file}:${hit.line}:${hit.column} ${hit.kind} "${hit.text}"`
    );
    expect(hits).toEqual([]);
  });

  it('scans the workspace tree and every hosted file', () => {
    const files = workspaceTsxFiles();
    expect(files).toContain(`${WORKSPACE_DIR}/WorkflowWorkspace.tsx`);
    expect(files.some((file) => file.includes('__tests__'))).toBe(false);
    for (const file of HOSTED_FILES) {
      expect(fs.existsSync(path.join(srcDir, file))).toBe(true);
    }
  });
});

describe('workspace message keys', () => {
  // A text has one key. "Loading run..." and "Loading run…" read the same
  // but get two catalog entries, so the screens drift apart.
  it('has no key with both a three-dot and an ellipsis form', () => {
    const known = new Set([
      ...Object.keys(workspaceMessages.en),
      ...Object.keys(baseStaticMessages.en),
    ]);
    const twins = Object.keys(workspaceMessages.en)
      .map((key) =>
        key.includes('…')
          ? [key, key.split('…').join('...')]
          : key.includes('...')
            ? [key, key.split('...').join('…')]
            : null
      )
      .filter((pair): pair is string[] => pair !== null)
      .filter(([, other]) => known.has(other as string))
      .map(([key, other]) => `${key} / ${other}`);
    expect(twins).toEqual([]);
  });
});
