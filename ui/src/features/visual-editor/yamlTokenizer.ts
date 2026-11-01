// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SyntaxTokenKind } from '@/lib/syntax-colors';

/** One coloured piece of a line. The pieces of a line join to the line. */
export interface YamlToken {
  kind: SyntaxTokenKind;
  text: string;
}

/** A line of YAML as tokens, with the mapping key that it defines, if any. */
export interface YamlLine {
  tokens: YamlToken[];
  /** The unquoted key of the line, for example `url` in `  url: ""`. */
  key?: string;
  /** The number of leading spaces. */
  indent: number;
}

const KEY_RE =
  /^(?:"(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s#'"[\]{},&*!|>%@`:-][^#]*?|-[^\s:][^#]*?)(?=:(?:\s|$))/;
const NUMBER_RE =
  /^(?:[-+]?(?:\d[\d_]*)(?:\.\d*)?(?:[eE][-+]?\d+)?|0x[0-9a-fA-F]+|0o[0-7]+|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$/;
const KEYWORD_RE = /^(?:true|True|TRUE|false|False|FALSE|null|Null|NULL|~)$/;
const VARIABLE_RE = /\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*/g;
const BLOCK_RE = /^[|>][+-]?\d?(?=\s|$)/;

function push(tokens: YamlToken[], kind: SyntaxTokenKind, text: string) {
  if (text === '') return;
  const last = tokens[tokens.length - 1];
  if (last && last.kind === kind) last.text += text;
  else tokens.push({ kind, text });
}

/** Splits a scalar into text and variable references (${NAME}, $NAME). */
function pushScalar(tokens: YamlToken[], kind: SyntaxTokenKind, text: string) {
  let from = 0;
  for (const match of text.matchAll(VARIABLE_RE)) {
    push(tokens, kind, text.slice(from, match.index));
    push(tokens, 'variable', match[0]);
    from = match.index + match[0].length;
  }
  push(tokens, kind, text.slice(from));
}

function quotedEnd(text: string, start: number): number {
  const quote = text[start];
  let i = start + 1;
  while (i < text.length) {
    if (quote === '"' && text[i] === '\\') i += 2;
    else if (quote === "'" && text[i] === "'" && text[i + 1] === "'") i += 2;
    else if (text[i] === quote) return i + 1;
    else i += 1;
  }
  return text.length;
}

/**
 * Tokenizes the value part of a line. Returns whether the value opens a
 * block scalar (`|` or `>`), whose lines follow.
 */
function tokenizeValue(text: string, tokens: YamlToken[]): boolean {
  let i = 0;
  let flow = 0;
  let opensBlock = false;
  let atStart = true;
  while (i < text.length) {
    const c = text[i]!;
    if (c === ' ' || c === '\t') {
      let j = i;
      while (j < text.length && (text[j] === ' ' || text[j] === '\t')) j += 1;
      push(tokens, 'plain', text.slice(i, j));
      i = j;
      continue;
    }
    if (c === '#' && (i === 0 || /\s/.test(text[i - 1]!))) {
      push(tokens, 'comment', text.slice(i));
      break;
    }
    if (c === '"' || c === "'") {
      const end = quotedEnd(text, i);
      // A quoted key inside a flow mapping: {"a": 1}.
      if (flow > 0 && text[end] === ':') {
        push(tokens, 'key', text.slice(i, end));
      } else {
        pushScalar(tokens, 'string', text.slice(i, end));
      }
      i = end;
    } else if (c === '{' || c === '[') {
      flow += 1;
      push(tokens, 'punct', c);
      i += 1;
    } else if ((c === '}' || c === ']') && flow > 0) {
      flow -= 1;
      push(tokens, 'punct', c);
      i += 1;
    } else if (c === ',' && flow > 0) {
      push(tokens, 'punct', c);
      i += 1;
    } else if (atStart && BLOCK_RE.test(text.slice(i))) {
      const indicator = BLOCK_RE.exec(text.slice(i))![0];
      push(tokens, 'punct', indicator);
      opensBlock = true;
      i += indicator.length;
    } else if (c === '&' || c === '*' || c === '!') {
      let j = i + 1;
      while (j < text.length && !/[\s,[\]{}]/.test(text[j]!)) j += 1;
      push(tokens, 'variable', text.slice(i, j));
      i = j;
    } else {
      // A plain scalar ends at a flow delimiter, at " #" or at the end.
      let j = i;
      while (j < text.length) {
        const d = text[j]!;
        if (flow > 0 && /[,[\]{}]/.test(d)) break;
        if (d === '#' && j > i && /\s/.test(text[j - 1]!)) break;
        if (flow > 0 && d === ':' && /\s|$/.test(text[j + 1] ?? ' ')) break;
        j += 1;
      }
      const word = text.slice(i, j).replace(/\s+$/, '');
      if (word === '') {
        // A stray character that no rule takes: keep it as plain text.
        push(tokens, 'plain', c);
        i += 1;
      } else {
        if (flow > 0 && text[j] === ':') {
          push(tokens, 'key', word);
        } else if (KEYWORD_RE.test(word)) {
          push(tokens, 'keyword', word);
        } else if (NUMBER_RE.test(word)) {
          push(tokens, 'number', word);
        } else {
          pushScalar(tokens, 'string', word);
        }
        i += word.length;
      }
    }
    atStart = false;
    if (flow > 0 && text[i] === ':') {
      push(tokens, 'punct', ':');
      i += 1;
    }
  }
  return opensBlock;
}

function unquote(raw: string): string {
  if (raw.startsWith('"') && raw.endsWith('"') && raw.length >= 2) {
    return raw.slice(1, -1).replace(/\\(.)/g, '$1');
  }
  if (raw.startsWith("'") && raw.endsWith("'") && raw.length >= 2) {
    return raw.slice(1, -1).replace(/''/g, "'");
  }
  return raw.trim();
}

/**
 * Splits YAML text into lines of coloured tokens, with the same kinds as the
 * Monaco themes: keys, strings, numbers, true/false/null, comments,
 * variable references and punctuation. It is a colouring aid for short
 * snippets, not a parser: the text of the tokens always joins back to the
 * input, whatever the input is.
 */
export function tokenizeYaml(source: string): YamlLine[] {
  const lines = source.split('\n');
  const result: YamlLine[] = [];
  // Column of the node that opened a block scalar, while its lines run.
  let blockParent: number | null = null;

  for (const line of lines) {
    const indent = line.length - line.trimStart().length;
    const tokens: YamlToken[] = [];

    if (blockParent !== null) {
      if (line.trim() === '' || indent > blockParent) {
        push(tokens, 'plain', line.slice(0, indent));
        pushScalar(tokens, 'string', line.slice(indent));
        result.push({ tokens, indent });
        continue;
      }
      blockParent = null;
    }

    if (line.trim() === '---' || line.trim() === '...') {
      push(tokens, 'plain', line.slice(0, indent));
      push(tokens, 'punct', line.trim());
      result.push({ tokens, indent });
      continue;
    }

    push(tokens, 'plain', line.slice(0, indent));
    let rest = line.slice(indent);
    // The column of the node that a block scalar of this line belongs to:
    // the key, else the last sequence marker.
    let parent = indent;
    // Sequence markers: "- ", also nested ("- - a").
    for (;;) {
      const marker = /^-(?:\s+|$)/.exec(rest);
      if (!marker) break;
      parent = line.length - rest.length;
      push(tokens, 'punct', '-');
      push(tokens, 'plain', marker[0].slice(1));
      rest = rest.slice(marker[0].length);
    }
    const keyMatch = KEY_RE.exec(rest);
    let key: string | undefined;
    if (keyMatch) {
      parent = line.length - rest.length;
      key = unquote(keyMatch[0]);
      push(tokens, 'key', keyMatch[0]);
      push(tokens, 'punct', ':');
      rest = rest.slice(keyMatch[0].length + 1);
    }
    const opensBlock = tokenizeValue(rest, tokens);
    if (opensBlock) blockParent = parent;
    result.push({ tokens, key, indent });
  }
  return result;
}

/**
 * Tokenizes one scalar value (text after a `key:`), with the same kinds as
 * `tokenizeYaml`. Other views use it to colour the value of a variable.
 */
export function tokenizeYamlValue(value: string): YamlToken[] {
  const tokens: YamlToken[] = [];
  tokenizeValue(value, tokens);
  return tokens;
}
