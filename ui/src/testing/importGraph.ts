// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Static import graph of the UI sources, for tests that keep heavy modules
 * out of a bundle chunk.
 *
 * @module testing/importGraph
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

/** One chain from a root to a forbidden module. Paths are relative to ui/src. */
export interface StaticImportHit {
  root: string;
  /** From the root to the forbidden module; packages are 'pkg:<name>'. */
  chain: string[];
}

/**
 * The modules that load Mermaid or that statically reach it on the run
 * page. The workflow workspace must not reach any of them.
 */
export const MERMAID_FORBIDDEN: Array<string | RegExp> = [
  /^pkg:mermaid$/,
  'components/ui/mermaid.tsx',
  'features/dags/components/visualization/Graph.tsx',
  'features/dags/components/visualization/DAGGraph.tsx',
  'features/dags/components/DAGStatus.tsx',
  'features/dags/components/dag-execution/DAGExecutionHistory.tsx',
];

const DEFAULT_SRC_DIR = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..'
);

const SOURCE_EXTENSIONS = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'];

function isSourceFile(file: string): boolean {
  return (
    SOURCE_EXTENSIONS.includes(path.extname(file)) && !file.endsWith('.d.ts')
  );
}

function isFile(file: string): boolean {
  try {
    return fs.statSync(file).isFile();
  } catch {
    return false;
  }
}

/** Package name of a bare specifier: 'react-dom/client' -> 'react-dom'. */
function packageName(specifier: string): string {
  const parts = specifier.split('/');
  if (specifier.startsWith('@') && parts.length > 1) {
    return `${parts[0]}/${parts[1]}`;
  }
  return parts[0] ?? specifier;
}

function resolveFile(base: string): string | null {
  if (isFile(base)) return base;
  for (const ext of SOURCE_EXTENSIONS) {
    if (isFile(base + ext)) return base + ext;
  }
  for (const ext of SOURCE_EXTENSIONS) {
    const index = path.join(base, `index${ext}`);
    if (isFile(index)) return index;
  }
  return null;
}

function hasOnlyTypeSpecifiers(
  elements: ts.NodeArray<ts.ImportSpecifier | ts.ExportSpecifier>
): boolean {
  return elements.length > 0 && elements.every((e) => e.isTypeOnly);
}

/**
 * Module specifiers of the static imports and re-exports of one file. A
 * declaration whose bindings are all type-only is left out, because the
 * compiler removes it. import() is an expression, not a declaration, so
 * dynamic imports and React.lazy are never listed.
 */
function staticSpecifiers(file: string): string[] {
  const text = fs.readFileSync(file, 'utf8');
  const kind = file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const source = ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    false,
    kind
  );
  const specifiers: string[] = [];
  for (const statement of source.statements) {
    if (ts.isImportDeclaration(statement)) {
      const clause = statement.importClause;
      if (clause?.isTypeOnly) continue;
      const bindings = clause?.namedBindings;
      if (
        clause &&
        !clause.name &&
        bindings &&
        ts.isNamedImports(bindings) &&
        hasOnlyTypeSpecifiers(bindings.elements)
      ) {
        continue;
      }
      if (ts.isStringLiteral(statement.moduleSpecifier)) {
        specifiers.push(statement.moduleSpecifier.text);
      }
    } else if (ts.isExportDeclaration(statement)) {
      if (statement.isTypeOnly || !statement.moduleSpecifier) continue;
      const clause = statement.exportClause;
      if (
        clause &&
        ts.isNamedExports(clause) &&
        hasOnlyTypeSpecifiers(clause.elements)
      ) {
        continue;
      }
      if (ts.isStringLiteral(statement.moduleSpecifier)) {
        specifiers.push(statement.moduleSpecifier.text);
      }
    } else if (
      ts.isImportEqualsDeclaration(statement) &&
      !statement.isTypeOnly &&
      ts.isExternalModuleReference(statement.moduleReference) &&
      ts.isStringLiteral(statement.moduleReference.expression)
    ) {
      specifiers.push(statement.moduleReference.expression.text);
    }
  }
  return specifiers;
}

/**
 * Finds the static import chains from the roots to the forbidden modules.
 *
 * Follows `import ... from` and `export ... from` only. Ignores
 * `import type`, `export type`, import() and React.lazy. Resolves '@/' to
 * the source directory, relative paths, source extensions and index files.
 * A bare specifier becomes the leaf 'pkg:<name>'. Returns one shortest chain
 * for each forbidden module that a root reaches.
 *
 * @param roots - files relative to the source directory, or absolute paths
 * @param forbidden - a string matches one module id exactly; a RegExp is
 *   tested against the module id
 * @param opts.srcDir - the source directory; ui/src when not given
 */
export function findForbiddenStaticImports(
  roots: string[],
  forbidden: Array<string | RegExp>,
  opts?: { srcDir?: string }
): StaticImportHit[] {
  const srcDir = path.resolve(opts?.srcDir ?? DEFAULT_SRC_DIR);
  const toId = (file: string) =>
    path.relative(srcDir, file).split(path.sep).join('/');
  const toFile = (id: string) => path.join(srcDir, ...id.split('/'));
  const isForbidden = (id: string) =>
    forbidden.some((rule) =>
      typeof rule === 'string' ? rule === id : rule.test(id)
    );

  const resolve = (from: string, specifier: string): string => {
    const bare = specifier.split('?')[0] ?? specifier;
    let base: string;
    if (bare.startsWith('@/')) {
      base = path.join(srcDir, bare.slice(2));
    } else if (bare.startsWith('.')) {
      base = path.resolve(path.dirname(from), bare);
    } else {
      return `pkg:${packageName(bare)}`;
    }
    return toId(resolveFile(base) ?? base);
  };

  const parent = new Map<string, string | null>();
  const queue: string[] = [];
  for (const root of roots) {
    const id = path.isAbsolute(root) ? toId(root) : root.replace(/\\/g, '/');
    if (parent.has(id)) continue;
    parent.set(id, null);
    queue.push(id);
  }

  const hits: StaticImportHit[] = [];
  for (let i = 0; i < queue.length; i++) {
    const id = queue[i]!;
    if (isForbidden(id)) {
      const chain: string[] = [];
      for (let at: string | null = id; at !== null; at = parent.get(at)!) {
        chain.unshift(at);
      }
      hits.push({ root: chain[0]!, chain });
    }
    if (id.startsWith('pkg:')) continue;
    const file = toFile(id);
    if (!isSourceFile(file) || !isFile(file)) continue;
    for (const specifier of staticSpecifiers(file)) {
      const next = resolve(file, specifier);
      if (parent.has(next)) continue;
      parent.set(next, id);
      queue.push(next);
    }
  }
  return hits;
}
