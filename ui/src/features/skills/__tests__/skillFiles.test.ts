// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  buildFileTree,
  extensionOf,
  filePaths,
  filePathProblem,
  formatBytes,
  imageType,
  languageForPath,
} from '../skillFiles';
import type { SkillFile } from '../skillIssues';

const file = (path: string, text = true, size = 10): SkillFile => ({
  path,
  size,
  sha256: path,
  text,
});

describe('buildFileTree', () => {
  it('lists SKILL.md first, then the three folders, also when they are empty', () => {
    const tree = buildFileTree([file('SKILL.md')]);
    expect(tree.map((node) => node.path)).toEqual([
      'SKILL.md',
      'references',
      'assets',
      'scripts',
    ]);
    expect(tree[1]).toMatchObject({ kind: 'dir', count: 0, children: [] });
  });

  it('nests the files and sorts the folders before the files', () => {
    const tree = buildFileTree([
      file('scripts/run.py'),
      file('references/b.md'),
      file('references/a.md'),
      file('references/deep/c.md'),
      file('assets/logo.png', false),
      file('SKILL.md'),
    ]);
    const references = tree.find((node) => node.path === 'references')!;
    expect(references).toMatchObject({ kind: 'dir', count: 3 });
    if (references.kind !== 'dir') throw new Error('dir');
    expect(references.children.map((node) => node.path)).toEqual([
      'references/deep',
      'references/a.md',
      'references/b.md',
    ]);
    expect(filePaths(tree)).toEqual([
      'SKILL.md',
      'references/deep/c.md',
      'references/a.md',
      'references/b.md',
      'assets/logo.png',
      'scripts/run.py',
    ]);
  });

  it('keeps a folder or a file of the root that the format does not know', () => {
    const tree = buildFileTree([
      file('SKILL.md'),
      file('README.md', false),
      file('extra/x.txt'),
    ]);
    expect(tree.map((node) => node.path)).toEqual([
      'SKILL.md',
      'references',
      'assets',
      'scripts',
      'extra',
      'README.md',
    ]);
  });
});

describe('language of a file', () => {
  it('is chosen by the extension', () => {
    const cases: Record<string, string> = {
      'references/a.md': 'markdown',
      'scripts/run.py': 'python',
      'scripts/run.sh': 'shell',
      'assets/data.json': 'json',
      'references/c.yaml': 'yaml',
      'references/c.YML': 'yaml',
      'scripts/a.js': 'javascript',
      'scripts/a.mjs': 'javascript',
      'scripts/a.ts': 'typescript',
      'references/notes.txt': 'plaintext',
      'references/noext': 'plaintext',
      'references/.gitkeep': 'plaintext',
      'references/archive.tar.gz': 'plaintext',
    };
    for (const [path, language] of Object.entries(cases)) {
      expect(languageForPath(path), path).toBe(language);
    }
    expect(extensionOf('a/b.C')).toBe('c');
    expect(extensionOf('a/.hidden')).toBe('');
  });

  it('knows the images that a browser shows', () => {
    expect(imageType('assets/logo.PNG')).toBe('image/png');
    expect(imageType('assets/a.jpg')).toBe('image/jpeg');
    expect(imageType('assets/a.svg')).toBeNull();
  });
});

describe('filePathProblem', () => {
  it('accepts a path under references, assets or scripts', () => {
    expect(filePathProblem('references/a.md')).toBeNull();
    expect(filePathProblem('assets/img/a.png')).toBeNull();
    expect(filePathProblem('scripts/.gitkeep')).toBeNull();
  });

  it('refuses a path that breaks the layout', () => {
    expect(filePathProblem('')).toBe('empty');
    expect(filePathProblem('SKILL.md')).toBe('skill-md');
    expect(filePathProblem('README.md')).toBe('folder');
    expect(filePathProblem('references')).toBe('folder');
    expect(filePathProblem('other/a.md')).toBe('folder');
    expect(filePathProblem('references//a.md')).toBe('syntax');
    expect(filePathProblem('references/../a.md')).toBe('syntax');
    expect(filePathProblem('/references/a.md')).toBe('syntax');
    expect(filePathProblem('references\\a.md')).toBe('syntax');
    expect(filePathProblem('references/.env')).toBe('hidden');
    expect(filePathProblem('references/.git/a')).toBe('hidden');
    expect(filePathProblem(`references/${'a'.repeat(600)}`)).toBe('too-long');
  });

  it('refuses a path in use', () => {
    expect(filePathProblem('references/a.md', ['references/a.md'])).toBe(
      'exists'
    );
  });
});

describe('formatBytes', () => {
  it('writes a short size', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(2 * 1024 * 1024)).toBe('2.0 MB');
  });
});
