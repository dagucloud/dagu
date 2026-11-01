// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  CST,
  Lexer,
  isNode,
  parseAllDocuments,
  parseDocument,
  visit,
} from 'yaml';
import type {
  Document,
  DocumentOptions,
  ParseOptions,
  Range,
  SchemaOptions,
} from 'yaml';

/**
 * Options of a parse. A line counter is not accepted: the parser can read a
 * copy of the text with other offsets.
 */
export type ServerParseOptions = Omit<ParseOptions, 'lineCounter'> &
  DocumentOptions &
  SchemaOptions;

/**
 * Parses all documents of the text as the server reads them. The server
 * reads a quoted scalar up to its closing quote, whatever the indent of its
 * later lines; YAML 1.2 needs those lines to be indented more than the
 * parent node. When such lines are the only errors, the documents are those
 * of the text with the lines indented, and the ranges of their nodes are
 * offsets of `text`. Otherwise they are the documents of `text`.
 */
export function parseAllDocumentsAsServer(
  text: string,
  options?: ServerParseOptions
): Document.Parsed[] {
  const docs = parseAllDocuments(text, options);
  if (!docs.some(hasErrors)) return docs;
  const repair = indentQuotedScalars(text);
  if (!repair) return docs;
  const repaired = parseAllDocuments(repair.text, options);
  if (repaired.some(hasErrors)) return docs;
  for (const doc of repaired) remapRanges(doc, repair.inserts);
  return repaired;
}

/** Like `parseAllDocumentsAsServer`, for a text of one document. */
export function parseDocumentAsServer(
  text: string,
  options?: ServerParseOptions
): Document.Parsed {
  const doc = parseDocument(text, options);
  if (!hasErrors(doc)) return doc;
  const repair = indentQuotedScalars(text);
  if (!repair) return doc;
  const repaired = parseDocument(repair.text, options);
  if (hasErrors(repaired)) return doc;
  remapRanges(repaired, repair.inserts);
  return repaired;
}

function hasErrors(doc: Document): boolean {
  return doc.errors.length > 0;
}

/** Spaces inserted at a line start. */
interface Insert {
  /** Offset of the line start in the original text. */
  at: number;
  /** Offset of the first inserted space in the repaired text. */
  from: number;
  count: number;
}

interface Repair {
  text: string;
  /** In text order. */
  inserts: Insert[];
}

const DOCUMENT_MARKER = /^(?:---|\.\.\.)(?:[ \t\r\n]|$)/;

/** A quoted scalar that goes on after the line of its opening quote. */
interface OpenScalar {
  /** Offset of the opening quote in the repaired text. */
  start: number;
  /** Offset of the closing quote in the original text. */
  close: number;
  /** Indent that its later lines need: past the opening quote. */
  indent: number;
}

/**
 * The text with the later lines of each multi-line quoted scalar indented
 * past its opening quote. Null when no line needs it, when a quoted scalar
 * has no closing quote or holds a document marker, or when the lexer does
 * not read an indented scalar as one quoted scalar: the quote starts a line
 * of a block scalar.
 *
 * The lexer reads the text line by line and waits at a quote until it has
 * the closing quote, so each line is indented before the lexer reads it.
 */
function indentQuotedScalars(text: string): Repair | null {
  const lexer = new Lexer();
  const inserts: Insert[] = [];
  let repaired = '';
  let shift = 0;
  let offset = 0;
  let marked = false;
  let open: OpenScalar | null = null;
  for (let pos = 0; pos < text.length; ) {
    const nl = text.indexOf('\n', pos);
    const end = nl === -1 ? text.length : nl + 1;
    let line = text.slice(pos, end);
    if (open && pos <= open.close) {
      if (DOCUMENT_MARKER.test(line)) return null;
      const spaces = /^ */.exec(line)![0].length;
      if (spaces < open.indent) {
        const count = open.indent - spaces;
        inserts.push({ at: pos, from: pos + shift, count });
        shift += count;
        line = ' '.repeat(count) + line;
      }
    }
    repaired += line;
    pos = end;
    for (const token of lexer.lex(line, pos < text.length)) {
      // The lexer marks the start of a document, a plain or block scalar
      // and a flow error with a control character that is not in the text.
      if (token === CST.SCALAR) {
        marked = true;
        continue;
      }
      if (token === CST.DOCUMENT || token === CST.FLOW_END) continue;
      if (open && offset >= open.start) {
        const read =
          offset === open.start &&
          !marked &&
          closingQuote(token, 0) === token.length - 1;
        if (!read) return null;
        open = null;
      }
      marked = false;
      offset += token.length;
    }
    const quote = repaired[offset];
    if (!open && (quote === "'" || quote === '"')) {
      const close = closingQuote(text, toOriginal(inserts, offset));
      if (close === undefined) return null;
      const indent = offset - repaired.lastIndexOf('\n', offset - 1);
      open = { start: offset, close, indent };
    }
  }
  return inserts.length > 0 && !open ? { text: repaired, inserts } : null;
}

/** Offset of the quote that closes the quoted scalar at `start`. */
function closingQuote(text: string, start: number): number | undefined {
  const quote = text[start];
  for (let i = start + 1; i < text.length; i++) {
    const c = text[i];
    if (quote === '"' && c === '\\') {
      i++;
    } else if (c === quote) {
      if (quote === "'" && text[i + 1] === "'") i++;
      else return i;
    }
  }
  return undefined;
}

/** The offset of the original text for an offset of the repaired text. */
function toOriginal(inserts: Insert[], offset: number): number {
  let low = 0;
  let high = inserts.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (inserts[mid]!.from <= offset) low = mid + 1;
    else high = mid;
  }
  const last = inserts[low - 1];
  if (!last) return offset;
  if (offset < last.from + last.count) return last.at;
  return offset - (last.from - last.at) - last.count;
}

function remapRanges(doc: Document, inserts: Insert[]): void {
  const remap = (range: Range): Range => [
    toOriginal(inserts, range[0]),
    toOriginal(inserts, range[1]),
    toOriginal(inserts, range[2]),
  ];
  if (doc.range) doc.range = remap(doc.range);
  visit(doc, (_, node) => {
    if (isNode(node) && node.range) node.range = remap(node.range);
  });
}
