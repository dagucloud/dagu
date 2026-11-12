// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isSeq, parseAllDocuments, parseDocument } from 'yaml';

/** One entry of `steps` that cannot be read alone. Offsets are UTF-16 indexes of the text. */
export interface BrokenItem {
  /** Index of the entry in the `steps` list. */
  seqIndex: number;
  /** Start of the content of the entry: the first character after "- ". */
  from: number;
  /** End of the last line of the entry that has content. */
  to: number;
  message: string;
  line: number;
  column: number;
  /** Name of the step, when the text still gives it. */
  name?: string;
  /** Dependencies of the step, when the text still gives them. */
  depends?: string[];
}

export interface Salvage {
  /**
   * The text with the content of each broken entry replaced by a small valid
   * mapping. It has the length and the line breaks of the text, so offsets
   * and lines of the two agree.
   */
  padded: string;
  /** Like `padded`, with each broken entry replaced by `{}`: the text that edits run on. */
  shrunk: string;
  /** Where the broken entries are in `shrunk`, for the mapping of edits back. */
  pieces: { shrunkFrom: number; shrunkTo: number; from: number; to: number }[];
  items: BrokenItem[];
}

interface Line {
  start: number;
  /** End of the line without its break. */
  end: number;
  text: string;
}

function splitLines(text: string): Line[] {
  const lines: Line[] = [];
  let start = 0;
  while (start <= text.length) {
    let end = text.indexOf('\n', start);
    const next = end === -1 ? text.length + 1 : end + 1;
    if (end === -1) end = text.length;
    const stop = end > start && text.charAt(end - 1) === '\r' ? end - 1 : end;
    lines.push({ start, end: stop, text: text.slice(start, stop) });
    start = next;
  }
  return lines;
}

function isBlankOrComment(line: string): boolean {
  const trimmed = line.trim();
  return trimmed === '' || trimmed.startsWith('#');
}

const STEPS_KEY = /^steps[ \t]*:[ \t]*(#.*)?$/;
const SEPARATOR = /^(---|\.\.\.)([ \t]|$)/;
const DASH = /^( *)-([ \t]|$)/;

interface Segment {
  startLine: number;
  /** Index of the last line of the entry that has content. */
  endLine: number;
  indent: number;
}

/** Splits the block list under a top level `steps:` into its entries. */
function segment(lines: Line[]): Segment[] | null {
  let first = -1;
  let seenContent = false;
  for (let i = 0; i < lines.length; i++) {
    const text = lines[i]!.text;
    if (SEPARATOR.test(text) && seenContent) return null;
    if (!isBlankOrComment(text) && !SEPARATOR.test(text)) seenContent = true;
    if (STEPS_KEY.test(text)) {
      first = i;
      break;
    }
  }
  if (first < 0) return null;
  let indent = -1;
  const segments: Segment[] = [];
  let current: Segment | null = null;
  for (let i = first + 1; i < lines.length; i++) {
    const text = lines[i]!.text;
    if (isBlankOrComment(text)) continue;
    if (SEPARATOR.test(text)) break;
    const dash = DASH.exec(text);
    const spaces = /^ */.exec(text)![0].length;
    if (indent < 0) {
      if (!dash) return null;
      indent = dash[1]!.length;
    }
    if (dash && dash[1]!.length === indent) {
      current = { startLine: i, endLine: i, indent };
      segments.push(current);
      continue;
    }
    // A line that is not deeper than the list ends it.
    const tabbed = text.charAt(spaces) === '\t';
    if (!tabbed && (spaces < indent || (spaces === indent && indent === 0))) {
      break;
    }
    if (!current) return null;
    current.endLine = i;
  }
  return segments.length > 0 ? segments : null;
}

function plain(message: string): string {
  const first = message.split('\n')[0] ?? message;
  return first.replace(/ at line \d+, column \d+:?$/, '');
}

function positionOf(
  lines: Line[],
  offset: number
): { line: number; column: number } {
  let low = 0;
  let high = lines.length - 1;
  while (low < high) {
    const mid = (low + high + 1) >> 1;
    if (lines[mid]!.start <= offset) low = mid;
    else high = mid - 1;
  }
  return { line: low + 1, column: offset - lines[low]!.start + 1 };
}

/** The first error of an entry parsed alone, or null when it is a single readable entry. */
function entryError(source: string): { message: string; pos: number } | null {
  let doc;
  try {
    doc = parseDocument(source);
  } catch (error) {
    return {
      message: error instanceof Error ? plain(error.message) : String(error),
      pos: 0,
    };
  }
  const error = doc.errors[0];
  if (error) return { message: plain(error.message), pos: error.pos[0] };
  if (!isSeq(doc.contents) || doc.contents.items.length !== 1) {
    return { message: 'The entry is not a single list item', pos: 0 };
  }
  return null;
}

function scalarOf(raw: string): string | undefined {
  const value = raw.trim();
  if (value === '') return undefined;
  const double = /^"((?:[^"\\]|\\.)*)"$/.exec(value);
  if (double) {
    try {
      return JSON.parse(`"${double[1]}"`) as string;
    } catch {
      return undefined;
    }
  }
  const single = /^'((?:[^']|'')*)'$/.exec(value);
  if (single) return single[1]!.replace(/''/g, "'");
  const special = /^[^\s[\]{}&*!|>'"%@`#,]/.test(value);
  const indicator = /^[-?:]\s/.test(value);
  const unsafe = /[#,[\]{}]/.test(value) || /:(\s|$)/.test(value);
  return special && !indicator && !unsafe ? value : undefined;
}

/** Reads `name:` and `depends:` of an entry that does not parse, when they are plain. */
function readFields(
  lines: Line[],
  segment: Segment
): { name?: string; depends?: string[] } {
  const body: string[] = [];
  for (let i = segment.startLine; i <= segment.endLine; i++) {
    body.push(lines[i]!.text);
  }
  const head = DASH.exec(body[0]!);
  if (!head) return {};
  let keyIndent = -1;
  const rest = body[0]!.slice(head[0].length);
  if (rest.trim() !== '') {
    body[0] = ' '.repeat(head[0].length) + rest;
    keyIndent = head[0].length + /^ */.exec(rest)![0].length;
  } else {
    const next = body.slice(1).find((l) => !isBlankOrComment(l));
    keyIndent = next ? /^ */.exec(next)![0].length : -1;
  }
  if (keyIndent < 0) return {};
  const atKey = (line: string) =>
    /^ */.exec(line)![0].length === keyIndent && !isBlankOrComment(line);
  const out: { name?: string; depends?: string[] } = {};
  for (let i = 0; i < body.length; i++) {
    const line = body[i]!;
    if (!atKey(line)) continue;
    const content = line.slice(keyIndent);
    const name = /^name[ \t]*:(.*)$/.exec(content);
    if (name && out.name === undefined) {
      out.name = scalarOf(name[1]!.replace(/\s+#.*$/, ''));
      continue;
    }
    const depends = /^depends[ \t]*:(.*)$/.exec(content);
    if (!depends || out.depends !== undefined) continue;
    const value = depends[1]!.replace(/\s+#.*$/, '').trim();
    const names: (string | undefined)[] = [];
    if (value.startsWith('[') && value.endsWith(']')) {
      const inner = value.slice(1, -1).trim();
      if (inner !== '') names.push(...inner.split(',').map(scalarOf));
    } else if (value === '') {
      for (let j = i + 1; j < body.length; j++) {
        const item = body[j]!;
        if (isBlankOrComment(item)) continue;
        const entry = /^( *)-[ \t]+(.*)$/.exec(item);
        if (!entry || entry[1]!.length < keyIndent) break;
        names.push(scalarOf(entry[2]!.replace(/\s+#.*$/, '')));
      }
    } else {
      names.push(scalarOf(value));
    }
    if (names.every((n): n is string => n !== undefined)) {
      out.depends = names as string[];
    }
  }
  return out;
}

/**
 * Finds the entries of `steps` that have a YAML error of their own and
 * replaces each by a valid mapping, so the rest of the file reads normally.
 * Gives null when the text cannot be read that way: the errors are outside
 * the entries of a block list `steps`, or an entry is too small to replace.
 */
export function salvageText(text: string): Salvage | null {
  const lines = splitLines(text);
  const segments = segment(lines);
  if (!segments) return null;
  const items: BrokenItem[] = [];
  segments.forEach((seg, seqIndex) => {
    const first = lines[seg.startLine]!;
    const last = lines[seg.endLine]!;
    const source = text.slice(first.start, last.end);
    const error = entryError(source);
    if (!error) return;
    const at = positionOf(lines, first.start + error.pos);
    const item: BrokenItem = {
      seqIndex,
      from: first.start + seg.indent + 1,
      to: last.end,
      message: error.message,
      line: at.line,
      column: at.column,
    };
    const fields = readFields(lines, seg);
    if (fields.name !== undefined) item.name = fields.name;
    if (fields.depends !== undefined) item.depends = fields.depends;
    items.push(item);
  });
  if (items.length === 0) return null;

  let padded = '';
  let shrunk = '';
  const pieces: Salvage['pieces'] = [];
  let pos = 0;
  for (const item of items) {
    // The placeholder stays on the first line of the entry: the line
    // breaks of the text do not move.
    const region = text.slice(item.from, item.to);
    const room = /^[^\r\n]*/.exec(region)![0].length;
    const holder = ' {}';
    if (room < holder.length) return null;
    const head = text.slice(pos, item.from);
    padded += head + holder;
    padded += region.slice(holder.length).replace(/[^\r\n]/g, ' ');
    shrunk += head;
    const shrunkFrom = shrunk.length;
    shrunk += ' {}';
    pieces.push({
      shrunkFrom,
      shrunkTo: shrunk.length,
      from: item.from,
      to: item.to,
    });
    pos = item.to;
  }
  padded += text.slice(pos);
  shrunk += text.slice(pos);

  const doc = parseAllDocuments(padded)[0];
  if (!doc || doc.errors.length > 0) return null;
  return { padded, shrunk, pieces, items };
}
