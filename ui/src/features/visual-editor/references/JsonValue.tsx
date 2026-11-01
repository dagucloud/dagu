// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useState, type ReactElement } from 'react';
import { CopyButton } from '@/components/ui/copy-button';
import { useI18n } from '@/i18n/I18nProvider';
import { SYNTAX_CLASS, type SyntaxTokenKind } from '@/lib/syntax-colors';
import { cn } from '@/lib/utils';

/** A value longer than this many characters is not parsed. */
export const JSON_PARSE_LIMIT = 200 * 1024;

/**
 * A prettified text longer than this many characters is not shown. The
 * indent grows with the depth, so a short, deep value can expand far past
 * the size of its text.
 */
const PRETTY_LIMIT = 1024 * 1024;

/** Values longer than this fold, with a Show more button. */
const FOLD_CHARS = 200;
const FOLD_LINES = 5;

export interface PrettyJson {
  /** The text to show. Equal to the value when it is not a JSON container. */
  pretty: string;
  /** True when the value is a JSON object or array. */
  isJson: boolean;
  /** True when the value is too long to parse. */
  tooLarge: boolean;
}

/**
 * Re-indents the text of a JSON container with two spaces, as
 * JSON.stringify(value, null, 2) does. The text of each string and each
 * number stays as it is, so a long integer keeps all its digits. Null when
 * the result would be longer than PRETTY_LIMIT.
 */
function indentJson(text: string): string | null {
  let out = '';
  let depth = 0;
  const pad = () => '\n' + '  '.repeat(depth);
  for (let i = 0; i < text.length; i++) {
    if (out.length > PRETTY_LIMIT) return null;
    const c = text[i] as string;
    if (c === '"') {
      let j = i + 1;
      while (j < text.length && text[j] !== '"') j += text[j] === '\\' ? 2 : 1;
      out += text.slice(i, j + 1);
      i = j;
    } else if (c === '{' || c === '[') {
      // An empty container stays on one line.
      let j = i + 1;
      while (/\s/.test(text[j] ?? '')) j++;
      if (text[j] === (c === '{' ? '}' : ']')) {
        out += c + text[j];
        i = j;
      } else {
        depth++;
        out += c + pad();
      }
    } else if (c === '}' || c === ']') {
      depth--;
      out += pad() + c;
    } else if (c === ',') {
      out += ',' + pad();
    } else if (c === ':') {
      out += ': ';
    } else if (!/\s/.test(c)) {
      out += c;
    }
  }
  return out.length > PRETTY_LIMIT ? null : out;
}

/**
 * The prettified text of a value that is a JSON object or array. A scalar
 * (a number, a bare JSON string, true, null), an invalid text, a text of
 * more than 200 KB and a text that would prettify to more than 1 MB stay as
 * they are. With `decoded`, the output is declared as JSON, so a text that
 * does not start with a bracket is parsed too; a scalar is still not
 * expanded.
 */
export function tryPrettyJson(
  value: string,
  options: { decoded?: boolean } = {}
): PrettyJson {
  const raw = { pretty: value, isJson: false, tooLarge: false };
  if (value.length > JSON_PARSE_LIMIT) return { ...raw, tooLarge: true };
  const trimmed = value.trim();
  const bracket = trimmed.startsWith('{') || trimmed.startsWith('[');
  if (!bracket && !options.decoded) return raw;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (typeof parsed !== 'object' || parsed === null) return raw;
  } catch {
    return raw;
  }
  const pretty = indentJson(trimmed);
  if (pretty === null) return { ...raw, tooLarge: true };
  return { pretty, isJson: true, tooLarge: false };
}

interface JsonPiece {
  kind: SyntaxTokenKind;
  text: string;
}

const PIECE_RE =
  /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?)|\b(true|false|null)\b|([{}[\],:])|(\s+)/g;

/** The pieces of a prettified text, by the colour of each. */
export function jsonPieces(pretty: string): JsonPiece[] {
  const pieces: JsonPiece[] = [];
  let last = 0;
  const push = (kind: SyntaxTokenKind, text: string) => {
    const before = pieces[pieces.length - 1];
    if (before && before.kind === kind) before.text += text;
    else pieces.push({ kind, text });
  };
  for (const m of pretty.matchAll(PIECE_RE)) {
    if (m.index > last) push('plain', pretty.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[1] !== undefined) {
      push(m[2] !== undefined ? 'key' : 'string', m[1]);
      if (m[2] !== undefined) push('punct', m[2]);
    } else if (m[3] !== undefined) push('number', m[3]);
    else if (m[4] !== undefined) push('keyword', m[4]);
    else if (m[5] !== undefined) push('punct', m[5]);
    else push('plain', m[0]);
  }
  if (last < pretty.length) push('plain', pretty.slice(last));
  return pieces;
}

const BUTTON =
  'rounded text-[10px] text-muted-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';

/**
 * The value of a step output. A JSON object or array shows prettified, in
 * monospace, with colours; a Raw button shows the original text. Any other
 * text shows as it is. A long value folds, with a Show more button, and
 * scrolls when open. The Raw and Copy buttons float on the top right corner
 * of the box of the value; the copy button copies the prettified text.
 */
export function JsonValue({
  value,
  decoded,
  className,
}: {
  value: string;
  /** The output is declared as JSON. */
  decoded?: boolean;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const [raw, setRaw] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const result = useMemo(
    () => tryPrettyJson(value, { decoded }),
    [value, decoded]
  );
  const shown = result.isJson && !raw ? result.pretty : value;
  const pieces = useMemo(
    () => (result.isJson && !raw ? jsonPieces(result.pretty) : null),
    [result, raw]
  );
  const long =
    shown.length > FOLD_CHARS || shown.split('\n').length > FOLD_LINES;
  return (
    <div
      data-testid="json-value"
      data-json={result.isJson}
      className={className}
    >
      {/* The box holds the value. The Raw and Copy buttons sit on its top right corner; the text has room for them. */}
      <div
        data-testid="json-value-box"
        className="relative mt-0.5 min-h-8 rounded bg-muted/40"
      >
        <pre
          className={cn(
            'whitespace-pre-wrap break-words rounded p-1 font-mono text-[11px] leading-4',
            result.isJson ? 'pr-16' : 'pr-8',
            expanded ? 'max-h-64 overflow-auto' : 'max-h-24 overflow-hidden'
          )}
        >
          {pieces
            ? pieces.map((piece, i) => (
                <span key={i} className={SYNTAX_CLASS[piece.kind]}>
                  {piece.text}
                </span>
              ))
            : shown}
        </pre>
        <div
          data-testid="json-value-corner"
          className="absolute right-1.5 top-1.5 flex items-center gap-1"
        >
          {result.isJson && (
            <button
              type="button"
              aria-pressed={raw}
              onClick={() => setRaw((on) => !on)}
              className={BUTTON}
            >
              {ts('Raw')}
            </button>
          )}
          <CopyButton
            text={result.isJson ? result.pretty : value}
            label={ts('Copy value')}
            size="sm"
            className="size-5 [&_svg]:size-3"
          />
        </div>
      </div>
      {result.tooLarge && (
        <p className="text-[10px] text-muted-foreground">
          {ts('Too large to format, shown as it is')}
        </p>
      )}
      {long && (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((open) => !open)}
          className={BUTTON}
        >
          {expanded ? ts('Show less') : ts('Show more')}
        </button>
      )}
    </div>
  );
}
