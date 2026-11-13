// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SKILL_FILE, type SkillFile } from './skillIssues';

/** The folders of a skill that hold files, in the order of the tree. */
export const SKILL_DIRS = ['references', 'assets', 'scripts'] as const;

/** The size limit of one file, in bytes. The server answers 413 above it. */
export const SKILL_FILE_MAX_BYTES = 4 * 1024 * 1024;

/** The longest relative path that the server accepts, in bytes. */
export const SKILL_PATH_MAX = 512;

export type FileNode =
  | {
      kind: 'dir';
      name: string;
      path: string;
      children: FileNode[];
      /** The number of files below. */
      count: number;
    }
  | { kind: 'file'; name: string; path: string; file: SkillFile };

function sortNodes(nodes: FileNode[]): FileNode[] {
  return nodes.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === 'dir' ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}

function countFiles(node: FileNode): number {
  return node.kind === 'file'
    ? 1
    : node.children.reduce((sum, child) => sum + countFiles(child), 0);
}

/**
 * Builds the tree of the files of a skill. SKILL.md comes first. The folders
 * `references`, `assets` and `scripts` are always there, also when empty, so
 * that a file can go in. Any other folder is shown where it is: the server
 * calls it an error and the user can remove its files. Files at the root come
 * after the folders.
 */
export function buildFileTree(files: readonly SkillFile[]): FileNode[] {
  const roots: FileNode[] = [];
  const dirs = new Map<string, Extract<FileNode, { kind: 'dir' }>>();

  const dirNode = (path: string): Extract<FileNode, { kind: 'dir' }> => {
    const found = dirs.get(path);
    if (found) return found;
    const slash = path.lastIndexOf('/');
    const node: Extract<FileNode, { kind: 'dir' }> = {
      kind: 'dir',
      name: path.slice(slash + 1),
      path,
      children: [],
      count: 0,
    };
    dirs.set(path, node);
    (slash === -1 ? roots : dirNode(path.slice(0, slash)).children).push(node);
    return node;
  };

  for (const dir of SKILL_DIRS) dirNode(dir);
  for (const file of files) {
    const slash = file.path.lastIndexOf('/');
    const node: FileNode = {
      kind: 'file',
      name: file.path.slice(slash + 1),
      path: file.path,
      file,
    };
    (slash === -1 ? roots : dirNode(file.path.slice(0, slash)).children).push(
      node
    );
  }

  const order = (node: FileNode): number => {
    if (node.path === SKILL_FILE) return 0;
    const index = (SKILL_DIRS as readonly string[]).indexOf(node.path);
    if (index !== -1) return 1 + index;
    return node.kind === 'dir' ? 10 : 20;
  };
  const sortDeep = (nodes: FileNode[]): FileNode[] => {
    for (const node of nodes) {
      if (node.kind === 'dir') {
        sortNodes(node.children);
        sortDeep(node.children);
        node.count = countFiles(node);
      }
    }
    return nodes;
  };
  sortDeep(roots);
  return roots.sort(
    (a, b) => order(a) - order(b) || a.name.localeCompare(b.name)
  );
}

/** The paths of the files below a node, in tree order. */
export function filePaths(nodes: readonly FileNode[]): string[] {
  return nodes.flatMap((node) =>
    node.kind === 'file' ? [node.path] : filePaths(node.children)
  );
}

const LANGUAGES: Record<string, string> = {
  md: 'markdown',
  markdown: 'markdown',
  py: 'python',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  html: 'html',
  css: 'css',
  xml: 'xml',
  sql: 'sql',
  toml: 'ini',
  ini: 'ini',
  go: 'go',
  rb: 'ruby',
  rs: 'rust',
  txt: 'plaintext',
};

/** The extension of a path in lower case, without the dot. Empty if none. */
export function extensionOf(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dot = name.lastIndexOf('.');
  return dot <= 0 ? '' : name.slice(dot + 1).toLowerCase();
}

/** The Monaco language of a file, chosen by its extension. */
export function languageForPath(path: string): string {
  return LANGUAGES[extensionOf(path)] ?? 'plaintext';
}

const IMAGES: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};

/** The media type of an image that a browser shows, or null. */
export function imageType(path: string): string | null {
  return IMAGES[extensionOf(path)] ?? null;
}

/** The last segment of a path. */
export function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1);
}

export type FilePathProblem =
  | 'empty'
  | 'too-long'
  | 'syntax'
  | 'skill-md'
  | 'folder'
  | 'hidden'
  | 'exists';

/**
 * Checks the path of a new file against the layout of the format: a file is
 * under references/, assets/ or scripts/, with clean segments and no hidden
 * name except `.gitkeep`. `exists` lists the paths in use.
 */
export function filePathProblem(
  path: string,
  exists: readonly string[] = []
): FilePathProblem | null {
  if (path === '') return 'empty';
  if (new TextEncoder().encode(path).length > SKILL_PATH_MAX) {
    return 'too-long';
  }
  if (path === SKILL_FILE) return 'skill-md';
  if (path.includes('\\') || path.startsWith('/')) return 'syntax';
  const parts = path.split('/');
  if (parts.some((part) => part === '' || part === '.' || part === '..')) {
    return 'syntax';
  }
  if (
    parts.length < 2 ||
    !(SKILL_DIRS as readonly string[]).includes(parts[0]!)
  ) {
    return 'folder';
  }
  const last = parts.length - 1;
  if (
    parts
      .slice(1)
      .some(
        (part, i) =>
          part.startsWith('.') && !(i + 1 === last && part === '.gitkeep')
      )
  ) {
    return 'hidden';
  }
  if (exists.includes(path)) return 'exists';
  return null;
}

/** A size in bytes as a short text: 512 B, 1.5 KB, 2.0 MB. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
