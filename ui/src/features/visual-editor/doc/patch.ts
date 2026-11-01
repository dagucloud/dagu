// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { parseDocument, type Document, type ToStringOptions } from 'yaml';
import type {
  EditErrorCode,
  EditResult,
  StepKey,
  TextEdit,
} from '../contracts';
import { parseAllDocumentsAsServer } from './serverYaml';

/**
 * Returns the offset where the text of document 0 ends: the line start of the
 * `---` marker that opens document 1 or of the `...` marker that closes
 * document 0, or the text length when there is no later document. Text from
 * this offset on is never part of an edit. `docs` is the result of
 * `parseAllDocumentsAsServer(text)`, when the caller has it already.
 */
export function documentZeroEnd(
  text: string,
  docs: Document[] = parseAllDocumentsAsServer(text)
): number {
  const range = docs.length > 1 ? docs[0]?.range : undefined;
  if (!range) return text.length;
  let end = Math.min(range[2], text.length);
  if (end > 0 && text[end - 1] !== '\n')
    end = text.lastIndexOf('\n', end - 1) + 1;
  const marker = text.lastIndexOf('\n', end - 2) + 1;
  if (/^\.\.\.(\s|$)/.test(text.slice(marker, end))) end = marker;
  return end;
}

/**
 * Applies edits to `text`. Edits refer to `text`, are sorted by `from` and do
 * not overlap. Throws a RangeError when this is not true.
 */
export function applyTextEdits(text: string, edits: TextEdit[]): string {
  let out = '';
  let pos = 0;
  for (const edit of edits) {
    if (edit.from < pos || edit.to < edit.from || edit.to > text.length) {
      throw new RangeError(
        `Invalid text edit ${edit.from}-${edit.to} after offset ${pos}`
      );
    }
    out += text.slice(pos, edit.from) + edit.insert;
    pos = edit.to;
  }
  return out + text.slice(pos);
}

/** One changed region of a line diff: `a[aStart, aEnd)` becomes `b[bStart, bEnd)`. */
export interface LineHunk {
  aStart: number;
  aEnd: number;
  bStart: number;
  bEnd: number;
}

/** Above this edit distance the diff gives one hunk for the whole middle part. */
const MAX_EDIT_DISTANCE = 3000;

/**
 * Line diff of `a` to `b`: the sorted hunks between matching lines. The
 * common prefix and suffix are cut first, then Myers' algorithm runs on the
 * middle part. For a very large change the middle part is one hunk.
 */
export function diffLines(
  a: readonly string[],
  b: readonly string[]
): LineHunk[] {
  const limit = Math.min(a.length, b.length);
  let prefix = 0;
  while (prefix < limit && a[prefix] === b[prefix]) prefix++;
  let suffix = 0;
  while (
    suffix < limit - prefix &&
    a[a.length - 1 - suffix] === b[b.length - 1 - suffix]
  ) {
    suffix++;
  }
  const aEnd = a.length - suffix;
  const bEnd = b.length - suffix;
  if (prefix === aEnd && prefix === bEnd) return [];
  const whole: LineHunk = { aStart: prefix, aEnd, bStart: prefix, bEnd };
  if (prefix === aEnd || prefix === bEnd) return [whole];

  const ids = new Map<string, number>();
  const idOf = (line: string): number => {
    let id = ids.get(line);
    if (id === undefined) {
      id = ids.size;
      ids.set(line, id);
    }
    return id;
  };
  const x = Int32Array.from(a.slice(prefix, aEnd), idOf);
  const y = Int32Array.from(b.slice(prefix, bEnd), idOf);
  const flags = myersFlags(x, y);
  if (!flags) return [whole];

  const hunks: LineHunk[] = [];
  let i = 0;
  let j = 0;
  while (i < x.length || j < y.length) {
    if (
      i < x.length &&
      j < y.length &&
      !flags.deleted[i] &&
      !flags.inserted[j]
    ) {
      i++;
      j++;
      continue;
    }
    const aStart = i;
    const bStart = j;
    while (
      (i < x.length && flags.deleted[i]) ||
      (j < y.length && flags.inserted[j])
    ) {
      if (i < x.length && flags.deleted[i]) i++;
      if (j < y.length && flags.inserted[j]) j++;
    }
    hunks.push({
      aStart: prefix + aStart,
      aEnd: prefix + i,
      bStart: prefix + bStart,
      bEnd: prefix + j,
    });
  }
  return hunks;
}

/** Myers' O(ND) diff. Returns flags of deleted `x` and inserted `y` items, or null above the limit. */
function myersFlags(
  x: Int32Array,
  y: Int32Array
): { deleted: Uint8Array; inserted: Uint8Array } | null {
  const n = x.length;
  const m = y.length;
  const max = Math.min(n + m, MAX_EDIT_DISTANCE);
  const offset = max + 1;
  const v = new Int32Array(2 * max + 3);
  // trace[d] is a copy of v for k in [-d-1, d+1], taken before round d.
  const trace: Int32Array[] = [];
  let steps = -1;
  for (let d = 0; d <= max && steps < 0; d++) {
    trace.push(v.slice(offset - d - 1, offset + d + 2));
    for (let k = -d; k <= d; k += 2) {
      const left = v[offset + k - 1]!;
      const right = v[offset + k + 1]!;
      let px = k === -d || (k !== d && left < right) ? right : left + 1;
      let py = px - k;
      while (px < n && py < m && x[px] === y[py]) {
        px++;
        py++;
      }
      v[offset + k] = px;
      if (px >= n && py >= m) {
        steps = d;
        break;
      }
    }
  }
  if (steps < 0) return null;

  const deleted = new Uint8Array(n);
  const inserted = new Uint8Array(m);
  let cx = n;
  let cy = m;
  for (let d = steps; d > 0; d--) {
    const prev = trace[d] as Int32Array;
    const at = (k: number) => prev[k + d + 1] ?? 0;
    const k = cx - cy;
    const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
    const prevK = down ? k + 1 : k - 1;
    const prevX = at(prevK);
    const prevY = prevX - prevK;
    if (down) inserted[prevY] = 1;
    else deleted[prevX] = 1;
    cx = prevX;
    cy = prevY;
  }
  return { deleted, inserted };
}

/** Lines of `text` without line ends. A final line end does not start a new line. */
export function splitLines(text: string): string[] {
  const lines = text.split('\n');
  if (lines[lines.length - 1] === '') lines.pop();
  return lines.map((line) => (line.endsWith('\r') ? line.slice(0, -1) : line));
}

/** Emit options that every re-emit uses: no folding and no padding in flow collections. */
export const BASE_EMIT_OPTIONS: ToStringOptions = {
  lineWidth: 0,
  flowCollectionPadding: false,
};

/** The emit options that fit a region best, with the text that the document emits with them. */
export interface EmitChoice {
  options: ToStringOptions;
  text: string;
  /** True when the emitted lines equal the lines of the region. */
  exact: boolean;
}

interface EmitStyle {
  indent: 2 | 4;
  indentSeq: boolean;
}

/** Cheap guess of the style of a region, to try the likely candidate first. */
function guessStyle(lines: string[]): EmitStyle {
  let minLead = Infinity;
  let flush = 0;
  let indented = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    const lead = line.length - line.trimStart().length;
    if (lead > 0 && lead < minLead && !line.trimStart().startsWith('#')) {
      minLead = lead;
    }
    const key = /^( *)[^\s#-].*:\s*(#.*)?$/.exec(line);
    const next = lines[i + 1];
    if (key && next !== undefined) {
      const pad = key[1]!;
      if (next.startsWith(`${pad}- `)) flush++;
      else if (
        next.startsWith(`${pad} `) &&
        next.trimStart().startsWith('- ')
      ) {
        indented++;
      }
    }
  }
  return {
    indent: Number.isFinite(minLead) && minLead >= 4 ? 4 : 2,
    indentSeq: flush <= indented,
  };
}

function contentLength(lines: string[]): number {
  let n = lines.length;
  while (n > 0 && lines[n - 1]!.trim() === '') n--;
  return n;
}

/** Line equality that ignores blank lines at the end: the emitter drops them. */
function sameLines(a: string[], b: string[]): boolean {
  const n = contentLength(a);
  return (
    n === contentLength(b) && a.slice(0, n).every((line, i) => line === b[i])
  );
}

function commonLineCount(a: string[], b: string[]): number {
  const counts = new Map<string, number>();
  for (const line of a) counts.set(line, (counts.get(line) ?? 0) + 1);
  let common = 0;
  for (const line of b) {
    const left = counts.get(line) ?? 0;
    if (left > 0) {
      counts.set(line, left - 1);
      common++;
    }
  }
  return common;
}

/**
 * Picks the `indent` and `indentSeq` values (on top of BASE_EMIT_OPTIONS)
 * that make `doc.toString` reproduce `region`, except for blank lines at the
 * end. The first candidate that reproduces every line wins. A candidate that
 * reproduces 90 percent of the lines wins too, to save emits. Otherwise the
 * candidate with the most equal lines wins; the first of equal candidates is
 * kept.
 */
export function chooseEmitOptions(doc: Document, region: string): EmitChoice {
  const regionLines = splitLines(region);
  const guess = guessStyle(regionLines);
  const styles: EmitStyle[] = [guess];
  for (const indent of [2, 4] as const) {
    for (const indentSeq of [true, false]) {
      if (indent !== guess.indent || indentSeq !== guess.indentSeq) {
        styles.push({ indent, indentSeq });
      }
    }
  }
  let best: (EmitChoice & { score: number }) | null = null;
  for (const style of styles) {
    const options: ToStringOptions = { ...BASE_EMIT_OPTIONS, ...style };
    const text = doc.toString(options);
    const lines = splitLines(text);
    if (sameLines(lines, regionLines)) return { options, text, exact: true };
    const score = commonLineCount(lines, regionLines);
    if (score >= 0.9 * regionLines.length) {
      return { options, text, exact: false };
    }
    if (!best || score > best.score)
      best = { options, text, exact: false, score };
  }
  const { options, text, exact } = best!;
  return { options, text, exact };
}

/** Lines of a region with their offsets, for building whole-line edits. */
interface LineTable {
  lines: string[];
  starts: number[];
  /** End of the content, before the line end. */
  contentEnds: number[];
  /** End of the line, after the line end. */
  ends: number[];
  eol: '\n' | '\r\n';
}

function lineTable(region: string): LineTable {
  const table: LineTable = {
    lines: [],
    starts: [],
    contentEnds: [],
    ends: [],
    eol: '\n',
  };
  const first = region.indexOf('\n');
  if (first > 0 && region[first - 1] === '\r') table.eol = '\r\n';
  let pos = 0;
  while (pos < region.length) {
    const nl = region.indexOf('\n', pos);
    const end = nl < 0 ? region.length : nl + 1;
    let contentEnd = nl < 0 ? region.length : nl;
    if (nl > pos && region[nl - 1] === '\r') contentEnd = nl - 1;
    table.lines.push(region.slice(pos, contentEnd));
    table.starts.push(pos);
    table.contentEnds.push(contentEnd);
    table.ends.push(end);
    pos = end;
  }
  return table;
}

/**
 * Comparison key for aligning original and re-emitted lines: the emitter
 * normalizes the gap before an inline comment and drops trailing spaces.
 */
function looseLine(line: string): string {
  return line.replace(/(\S)\s+#/, '$1 #').trimEnd();
}

const INLINE_COMMENT = /^(.*?\S)(\s+)(#.*)$/;

/**
 * Gives `edited` the gap before the inline comment of `original` when the
 * comment text is the same in `original`, `emitted` and `edited`.
 */
function keepCommentGap(
  original: string,
  emitted: string,
  edited: string
): string {
  const o = INLINE_COMMENT.exec(original);
  const x = INLINE_COMMENT.exec(emitted);
  const y = INLINE_COMMENT.exec(edited);
  if (!o || !x || !y || o[3] !== x[3] || x[3] !== y[3]) return edited;
  return y[1]! + o[2]! + y[3]!;
}

/** Whole-line replacement of `orig[os, oe)` by `lines`, in the line numbers of the original. */
interface LineChange {
  os: number;
  oe: number;
  lines: string[];
}

/**
 * Maps the changes of the re-emitted text `a` to `b` onto the original lines.
 * `a` is what `orig` would look like without drift (comment gaps, quotes
 * that the emitter normalizes). A change that touches lines where `orig` and
 * `a` differ grows to the nearest lines that match, so that drift only moves
 * lines next to the edit. Throws when the mapping is inconsistent.
 */
export function mapLineChanges(
  orig: string[],
  a: string[],
  b: string[]
): LineChange[] {
  const nO = orig.length;
  const nA = a.length;
  const aToO = new Int32Array(nA).fill(-1);
  let i = 0;
  let j = 0;
  const origKeys = orig.map(looseLine);
  const aKeys = a.map(looseLine);
  for (const h of diffLines(origKeys, aKeys)) {
    while (i < h.aStart) aToO[j++] = i++;
    i = h.aEnd;
    j = h.bEnd;
  }
  while (j < nA) aToO[j++] = i++;
  const matched = (k: number) => aToO[k]! >= 0;

  // prevM[p]: last matched line before p (or -1). nextM[p]: first matched line from p (or nA).
  const prevM = new Int32Array(nA + 1);
  const nextM = new Int32Array(nA + 1);
  prevM[0] = -1;
  for (let p = 1; p <= nA; p++)
    prevM[p] = matched(p - 1) ? p - 1 : prevM[p - 1]!;
  nextM[nA] = nA;
  for (let p = nA - 1; p >= 0; p--) nextM[p] = matched(p) ? p : nextM[p + 1]!;

  interface Extent {
    ws: number;
    we: number;
    bs: number;
    be: number;
  }
  const extents: Extent[] = [];
  const abHunks = diffLines(a, b);
  // Lines of `b` that the edit does not change keep the bytes of the original.
  const outB = b.slice();
  let ak = 0;
  let bk = 0;
  const keepOriginal = (from: number, hunkStart: number, bFrom: number) => {
    for (; from < hunkStart; from++, bFrom++) {
      if (matched(from)) outB[bFrom] = orig[aToO[from]!]!;
    }
  };
  for (const h of abHunks) {
    keepOriginal(ak, h.aStart, bk);
    // A changed line keeps the gap before its inline comment.
    const pairs = Math.min(h.aEnd - h.aStart, h.bEnd - h.bStart);
    for (let t = 0; t < pairs; t++) {
      const k = h.aStart + t;
      if (matched(k)) {
        const at = h.bStart + t;
        outB[at] = keepCommentGap(orig[aToO[k]!]!, a[k]!, b[at]!);
      }
    }
    ak = h.aEnd;
    bk = h.bEnd;
  }
  keepOriginal(ak, nA, bk);
  for (const h of abHunks) {
    let ws = h.aStart;
    let we = h.aEnd;
    if (ws === we) {
      if (ws > 0 && ws < nA && !matched(ws - 1) && !matched(ws)) {
        ws = prevM[ws]! + 1;
        we = nextM[we]!;
      }
    } else {
      if (ws > 0 && !matched(ws) && !matched(ws - 1)) ws = prevM[ws]! + 1;
      if (we < nA && !matched(we - 1) && !matched(we)) we = nextM[we]!;
    }
    const extent: Extent = {
      ws,
      we,
      bs: h.bStart - (h.aStart - ws),
      be: h.bEnd + (we - h.aEnd),
    };
    const last = extents[extents.length - 1];
    if (last && extent.ws <= last.we) {
      last.we = Math.max(last.we, extent.we);
      last.be = extent.be;
    } else {
      extents.push(extent);
    }
  }

  const changes: LineChange[] = [];
  let floor = 0;
  for (const { ws, we, bs, be } of extents) {
    let os: number;
    let oe: number;
    if (ws === we) {
      if (ws === 0) os = 0;
      else if (matched(ws - 1)) os = aToO[ws - 1]! + 1;
      else if (ws === nA) os = nO;
      else os = aToO[ws]!;
      oe = os;
    } else {
      if (matched(ws)) os = aToO[ws]!;
      else if (ws === 0) os = 0;
      else os = aToO[ws - 1]! + 1;
      if (matched(we - 1)) oe = aToO[we - 1]! + 1;
      else if (we === nA) oe = nO;
      else oe = aToO[we]!;
    }
    const lines = outB.slice(bs, be);
    while (os < oe && lines.length > 0 && orig[os] === lines[0]) {
      os++;
      lines.shift();
    }
    while (
      os < oe &&
      lines.length > 0 &&
      orig[oe - 1] === lines[lines.length - 1]
    ) {
      oe--;
      lines.pop();
    }
    if (os === oe && lines.length === 0) continue;
    if (os < floor || oe < os || os < 0 || oe > nO) {
      throw new RangeError('Inconsistent line mapping');
    }
    changes.push({ os, oe, lines });
    floor = oe;
  }
  return changes;
}

/** Turns line changes into character edits against the text of the region. */
function toTextEdits(table: LineTable, changes: LineChange[]): TextEdit[] {
  const n = table.lines.length;
  const unterminated = n > 0 && table.ends[n - 1] === table.contentEnds[n - 1];
  const { eol } = table;
  return changes.map(({ os, oe, lines }) => {
    const atEnd = oe === n && unterminated;
    if (oe === os) {
      if (os === n) {
        const pos = n === 0 ? 0 : table.ends[n - 1]!;
        if (atEnd) {
          const at = table.contentEnds[n - 1]!;
          return { from: at, to: at, insert: eol + lines.join(eol) };
        }
        return {
          from: pos,
          to: pos,
          insert: lines.map((l) => l + eol).join(''),
        };
      }
      const at = table.starts[os]!;
      return { from: at, to: at, insert: lines.map((l) => l + eol).join('') };
    }
    if (atEnd) {
      if (lines.length === 0 && os > 0) {
        return {
          from: table.contentEnds[os - 1]!,
          to: table.ends[n - 1]!,
          insert: '',
        };
      }
      return {
        from: table.starts[os]!,
        to: table.ends[n - 1]!,
        insert: lines.join(eol),
      };
    }
    return {
      from: table.starts[os]!,
      to: table.ends[oe - 1]!,
      insert: lines.map((l) => l + eol).join(''),
    };
  });
}

/** Result of a mutation callback; see `editDocument`. */
export type MutateOutcome =
  | void
  | { ok: true; select?: StepKey }
  | { ok: false; code: EditErrorCode; message: string };

/** `editDocument` result with a flag that tells whether the patch fell back to the whole region. */
export interface DetailedEditResult {
  result: EditResult;
  /** True when hunk mapping failed or did not verify and the whole document 0 was replaced. */
  fallback: boolean;
}

function sameValue(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  // An integer reads as a number, or as a bigint where it was parsed with
  // `intAsBigInt`: the two match at float precision.
  if (typeof a === 'number' && typeof b === 'bigint') return a === Number(b);
  if (typeof a === 'bigint' && typeof b === 'number') return Number(a) === b;
  if (typeof a !== 'object' || typeof b !== 'object' || !a || !b) return false;
  if (a instanceof Map && b instanceof Map) {
    if (a.size !== b.size) return false;
    const left = [...a];
    const right = [...b];
    return left.every(
      ([k, v], idx) =>
        sameValue(k, right[idx]![0]) && sameValue(v, right[idx]![1])
    );
  }
  if (a instanceof Set && b instanceof Set) {
    return sameValue([...a], [...b]);
  }
  if (a instanceof Date && b instanceof Date)
    return a.getTime() === b.getTime();
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  return keysA.every(
    (key, idx) =>
      key === keysB[idx] &&
      sameValue(
        (a as Record<string, unknown>)[key],
        (b as Record<string, unknown>)[key]
      )
  );
}

/** True when `text` parses to the same documents as `expected` in document 0. */
function verifies(text: string, count: number, expected: Document): boolean {
  let expectedValue: unknown;
  try {
    expectedValue = expected.toJS();
  } catch {
    return true; // Cannot compare values (for example an unresolved alias).
  }
  try {
    const docs = parseAllDocumentsAsServer(text);
    const doc = docs[0];
    if (docs.length !== count || !doc || doc.errors.length > 0) return false;
    return sameValue(doc.toJS(), expectedValue);
  } catch {
    return false;
  }
}

function failure(code: EditErrorCode, message: string): DetailedEditResult {
  return { result: { ok: false, code, message }, fallback: false };
}

/**
 * Like `editDocument`, and reports whether the whole-region fallback was used.
 * Tests use the flag to prove that the hunk mapping holds on a corpus.
 */
export function editDocumentDetailed(
  text: string,
  mutate: (doc: Document) => MutateOutcome
): DetailedEditResult {
  const docs = parseAllDocumentsAsServer(text);
  const doc = docs[0] ?? parseDocument(text);
  const count = Math.max(docs.length, 1);
  const firstError = doc.errors[0];
  if (firstError) return failure('parseError', firstError.message);

  const end = documentZeroEnd(text, docs);
  const region = text.slice(0, end);

  // The document comes from the parse above, so the mutation can run in place.
  const edited = doc;
  let choice: EmitChoice;
  let select: StepKey | undefined;
  let after: string;
  try {
    // The emit throws for a text that parses, such as an alias before its anchor.
    choice = chooseEmitOptions(doc, region);
    const outcome = mutate(edited);
    if (outcome && !outcome.ok) {
      return failure(outcome.code, outcome.message);
    }
    if (outcome?.ok) select = outcome.select;
    after = edited.toString(choice.options);
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    return failure('invalidYaml', `The edit gives invalid YAML: ${reason}`);
  }
  const before = choice.text;
  if (after === before)
    return failure('noop', 'The edit does not change the document.');

  const table = lineTable(region);
  let edits: TextEdit[] | null = null;
  try {
    const changes = mapLineChanges(
      table.lines,
      splitLines(before),
      splitLines(after)
    );
    edits = toTextEdits(table, changes);
    if (edits.length === 0) {
      return failure('noop', 'The edit does not change the document.');
    }
    const patched = applyTextEdits(text, edits);
    const delta = edits.reduce(
      (n, e) => n + e.insert.length - (e.to - e.from),
      0
    );
    const holds = choice.exact
      ? sameLines(splitLines(patched.slice(0, end + delta)), splitLines(after))
      : verifies(patched, count, edited);
    if (!holds) edits = null;
  } catch {
    edits = null;
  }
  let fallback = false;
  if (!edits) {
    fallback = true;
    const body = splitLines(after).join(table.eol);
    const insert = after.length === 0 ? '' : body + table.eol;
    edits = [{ from: 0, to: end, insert }];
    if (insert === region) {
      return failure('noop', 'The edit does not change the document.');
    }
  }
  const result: EditResult = {
    ok: true,
    text: applyTextEdits(text, edits),
    edits,
  };
  if (select !== undefined) result.select = select;
  return { result, fallback };
}

/**
 * Parses `text`, runs `mutate` on document 0 and returns the
 * whole-line edits that turn `text` into the edited document. The edits
 * touch only lines next to the change: they come from a diff of two
 * re-emits (unedited and edited), mapped back onto the original lines.
 * Document 1 and later are never part of an edit.
 */
export function editDocument(
  text: string,
  mutate: (doc: Document) => MutateOutcome
): EditResult {
  return editDocumentDetailed(text, mutate).result;
}
