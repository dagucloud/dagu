// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';

/**
 * Regular expression source matching ANSI escape sequences.
 * Credit: https://github.com/chalk/ansi-regex/commit/02fa893d619d3da85411acc8fd4e2eea0e95a9d9 under MIT license
 */
export const ANSI_CODES_REGEX = [
  '[\\u001B\\u009B][[\\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]+)*|[a-zA-Z\\d]+(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]*)*)?\\u0007)',
  '(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]))',
].join('|');

/** Removes all ANSI escape sequences from the text. */
export function stripAnsi(text: string): string {
  return text.replace(new RegExp(ANSI_CODES_REGEX, 'g'), '');
}

type TextStyle = {
  color?: string;
  background?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
};

export type AnsiSegment = {
  text: string;
  style: TextStyle;
};

// Base 16 palette tuned to stay legible on both light and dark backgrounds.
const BASE_COLORS: Record<number, string> = {
  0: '#6b7280',
  1: '#dc2626',
  2: '#16a34a',
  3: '#b45309',
  4: '#2563eb',
  5: '#9333ea',
  6: '#0891b2',
  7: '#9ca3af',
  8: '#6b7280',
  9: '#ef4444',
  10: '#22c55e',
  11: '#d97706',
  12: '#3b82f6',
  13: '#a855f7',
  14: '#06b6d4',
  15: '#d1d5db',
};

function color256(n: number): string | undefined {
  if (!Number.isFinite(n) || n < 0 || n > 255) return undefined;
  if (n < 16) return BASE_COLORS[n];
  if (n < 232) {
    const idx = n - 16;
    const steps = [0, 95, 135, 175, 215, 255];
    const r = steps[Math.floor(idx / 36)];
    const g = steps[Math.floor(idx / 6) % 6];
    const b = steps[idx % 6];
    return `rgb(${r},${g},${b})`;
  }
  const gray = 8 + 10 * (n - 232);
  return `rgb(${gray},${gray},${gray})`;
}

function applySgr(prev: TextStyle, params: string): TextStyle {
  let style: TextStyle = { ...prev };
  const codes = params === '' ? [0] : params.split(';').map(Number);

  for (let i = 0; i < codes.length; i++) {
    const code = codes[i] ?? 0;
    switch (code) {
      case 0:
        style = {};
        break;
      case 1:
        style.bold = true;
        break;
      case 2:
        style.dim = true;
        break;
      case 3:
        style.italic = true;
        break;
      case 4:
        style.underline = true;
        break;
      case 21:
      case 22:
        delete style.bold;
        delete style.dim;
        break;
      case 23:
        delete style.italic;
        break;
      case 24:
        delete style.underline;
        break;
      case 39:
        delete style.color;
        break;
      case 49:
        delete style.background;
        break;
      case 38:
      case 48: {
        const target = code === 38 ? 'color' : 'background';
        const mode = codes[i + 1];
        if (mode === 5) {
          const value = color256(codes[i + 2] ?? -1);
          if (value) style[target] = value;
          i += 2;
        } else if (mode === 2) {
          const [r, g, b] = [codes[i + 2], codes[i + 3], codes[i + 4]];
          if ([r, g, b].every((v) => Number.isFinite(v))) {
            style[target] = `rgb(${r},${g},${b})`;
          }
          i += 4;
        }
        break;
      }
      default:
        if (code >= 30 && code <= 37) style.color = BASE_COLORS[code - 30];
        else if (code >= 90 && code <= 97)
          style.color = BASE_COLORS[code - 90 + 8];
        else if (code >= 40 && code <= 47)
          style.background = BASE_COLORS[code - 40];
        else if (code >= 100 && code <= 107)
          style.background = BASE_COLORS[code - 100 + 8];
        break;
    }
  }

  return style;
}

// Select Graphic Rendition sequences are the only ones that affect styling.
const SGR_REGEX_SOURCE = '^[\\u001B\\u009B]\\[([0-9;]*)m$';
const SGR_RE = new RegExp(SGR_REGEX_SOURCE);

/**
 * Parses a single line into styled segments. Non-SGR escape sequences are
 * dropped; SGR sequences update the style carried by subsequent segments.
 */
export function parseAnsi(line: string): AnsiSegment[] {
  const segments: AnsiSegment[] = [];
  const re = new RegExp(ANSI_CODES_REGEX, 'g');
  let style: TextStyle = {};
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = re.exec(line)) !== null) {
    if (match.index > lastIndex) {
      segments.push({ text: line.slice(lastIndex, match.index), style });
    }
    const sgr = SGR_RE.exec(match[0]);
    if (sgr) {
      style = applySgr(style, sgr[1] ?? '');
    }
    lastIndex = re.lastIndex;
  }
  if (lastIndex < line.length) {
    segments.push({ text: line.slice(lastIndex), style });
  }
  return segments;
}

function toCssProperties(style: TextStyle): React.CSSProperties | undefined {
  const css: React.CSSProperties = {};
  if (style.color) css.color = style.color;
  if (style.background) css.backgroundColor = style.background;
  if (style.bold) css.fontWeight = 600;
  if (style.dim) css.opacity = 0.65;
  if (style.italic) css.fontStyle = 'italic';
  if (style.underline) css.textDecoration = 'underline';
  return Object.keys(css).length > 0 ? css : undefined;
}

function highlightParts(
  text: string,
  highlight: string,
  keyBase: string
): React.ReactNode[] {
  const parts: React.ReactNode[] = [];
  const lower = text.toLowerCase();
  const term = highlight.toLowerCase();
  let from = 0;
  let at = lower.indexOf(term, from);
  let part = 0;

  while (at !== -1) {
    if (at > from) parts.push(text.slice(from, at));
    parts.push(
      <mark
        key={`${keyBase}-${part++}`}
        className="rounded-[2px] bg-warning/50 text-foreground"
      >
        {text.slice(at, at + highlight.length)}
      </mark>
    );
    from = at + highlight.length;
    at = lower.indexOf(term, from);
  }
  if (from < text.length) parts.push(text.slice(from));
  return parts;
}

/**
 * Renders one log line with ANSI colors applied, optionally highlighting all
 * case-insensitive occurrences of a search term.
 */
export const AnsiLine = React.memo(function AnsiLine({
  text,
  highlight,
}: {
  text: string;
  highlight?: string;
}) {
  const segments = parseAnsi(text);
  return (
    <>
      {segments.map((segment, index) => (
        <span key={index} style={toCssProperties(segment.style)}>
          {highlight
            ? highlightParts(segment.text, highlight, String(index))
            : segment.text}
        </span>
      ))}
    </>
  );
});
