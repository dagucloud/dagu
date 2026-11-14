// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Finds user-visible text that is written in JSX without translation, for
 * tests that keep the UI in the message catalogs.
 *
 * @module testing/jsxLiterals
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

/** The attributes whose string literals a user reads or hears. */
export const TEXT_ATTRIBUTES = [
  'aria-label',
  'title',
  'placeholder',
  'alt',
] as const;

export type TextAttribute = (typeof TEXT_ATTRIBUTES)[number];

/** One literal. Lines and columns start at 1. */
export interface JsxLiteralHit {
  /** Path relative to the source directory, with '/' separators. */
  file: string;
  line: number;
  column: number;
  /** 'text' for JSX text, else the attribute that holds the literal. */
  kind: 'text' | TextAttribute;
  /** The literal, trimmed, with its inner white space collapsed. */
  text: string;
}

const DEFAULT_SRC_DIR = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..'
);

const LETTER = /\p{L}/u;
// JSX keeps character references such as '&gt;' as written: their names
// are not letters that a user reads.
const CHAR_REFERENCE = /&(?:#\d+|#x[\da-f]+|[a-z][a-z\d]*);/gi;
const IGNORE_MARKER = /(?:\/\/|\/\*)\s*i18n-ignore\b/;
// A line that holds only the marker comment covers the next line.
const MARKER_LINE = /^\s*\{?\s*(?:\/\/|\/\*)\s*i18n-ignore\b.*$/;

function isTextAttribute(name: string): name is TextAttribute {
  return (TEXT_ATTRIBUTES as readonly string[]).includes(name);
}

/**
 * The string literals that a JSX expression can give as its value: a
 * literal, the static parts of a template, the branches of ?:, ??, || and
 * &&, and the operands of +. A call such as ts('...') is translated, so it
 * is not entered.
 */
function literalLeaves(expression: ts.Expression): ts.Node[] {
  if (
    ts.isStringLiteral(expression) ||
    ts.isNoSubstitutionTemplateLiteral(expression)
  ) {
    return [expression];
  }
  if (ts.isTemplateExpression(expression)) {
    return [expression];
  }
  if (ts.isParenthesizedExpression(expression)) {
    return literalLeaves(expression.expression);
  }
  if (ts.isConditionalExpression(expression)) {
    return [
      ...literalLeaves(expression.whenTrue),
      ...literalLeaves(expression.whenFalse),
    ];
  }
  if (ts.isBinaryExpression(expression)) {
    const op = expression.operatorToken.kind;
    if (op === ts.SyntaxKind.AmpersandAmpersandToken) {
      return literalLeaves(expression.right);
    }
    if (
      op === ts.SyntaxKind.BarBarToken ||
      op === ts.SyntaxKind.QuestionQuestionToken ||
      op === ts.SyntaxKind.PlusToken
    ) {
      return [
        ...literalLeaves(expression.left),
        ...literalLeaves(expression.right),
      ];
    }
  }
  return [];
}

function literalText(node: ts.Node): string {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text;
  }
  if (ts.isTemplateExpression(node)) {
    return [
      node.head.text,
      ...node.templateSpans.map((span) => span.literal.text),
    ].join(' ');
  }
  if (ts.isJsxText(node)) {
    return node.text;
  }
  return '';
}

function scanFile(file: string, id: string): JsxLiteralHit[] {
  const text = fs.readFileSync(file, 'utf8');
  const source = ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS
  );
  const lines = text.split(/\r?\n/);
  const ignored = (line: number) =>
    IGNORE_MARKER.test(lines[line - 1] ?? '') ||
    MARKER_LINE.test(lines[line - 2] ?? '');

  const hits: JsxLiteralHit[] = [];
  const report = (node: ts.Node, kind: JsxLiteralHit['kind']) => {
    const value = literalText(node).replace(/\s+/g, ' ').trim();
    if (!LETTER.test(value.replace(CHAR_REFERENCE, ' '))) return;
    // JSX text starts with the white space before it: use the first letter.
    const raw = node.getText(source);
    const lead = raw.length - raw.trimStart().length;
    const at = source.getLineAndCharacterOfPosition(node.getStart() + lead);
    const line = at.line + 1;
    if (ignored(line)) return;
    hits.push({ file: id, line, column: at.character + 1, kind, text: value });
  };

  const visit = (node: ts.Node) => {
    if (ts.isJsxText(node)) {
      report(node, 'text');
    } else if (
      ts.isJsxExpression(node) &&
      node.expression &&
      !ts.isJsxAttribute(node.parent)
    ) {
      for (const leaf of literalLeaves(node.expression)) {
        report(leaf, 'text');
      }
    } else if (ts.isJsxAttribute(node)) {
      const name = node.name.getText(source);
      const init = node.initializer;
      if (isTextAttribute(name) && init) {
        if (ts.isStringLiteral(init)) {
          report(init, name);
        } else if (ts.isJsxExpression(init) && init.expression) {
          for (const leaf of literalLeaves(init.expression)) {
            report(leaf, name);
          }
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return hits;
}

/**
 * Finds the untranslated literals of JSX files.
 *
 * Reports JSX text that has a letter, string literals in JSX child
 * expressions such as `{on ? 'On' : 'Off'}`, and string literals in the
 * aria-label, title, placeholder and alt attributes. Text with only white
 * space, digits, punctuation or character references (`&gt;`) is not
 * reported. A hit on a line that has
 * the comment `// i18n-ignore` (or `{/* i18n-ignore *\/}`) is not reported.
 * A line that holds only that comment also covers the next line.
 *
 * @param paths - files relative to the source directory, or absolute paths
 * @param opts.srcDir - the source directory; ui/src when not given
 */
export function findJsxLiterals(
  paths: string[],
  opts?: { srcDir?: string }
): JsxLiteralHit[] {
  const srcDir = path.resolve(opts?.srcDir ?? DEFAULT_SRC_DIR);
  return paths.flatMap((entry) => {
    const file = path.isAbsolute(entry) ? entry : path.join(srcDir, entry);
    const id = path.relative(srcDir, file).split(path.sep).join('/');
    return scanFile(file, id);
  });
}
