// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { isMap, isNode } from 'yaml';
import type { Document } from 'yaml';
import {
  parseAllDocumentsAsServer,
  parseDocumentAsServer,
} from '../serverYaml';

/** The text of the value of `key` in the root mapping. */
function valueText(doc: Document, text: string, key: string): string {
  const node = isMap(doc.contents) ? doc.contents.get(key, true) : undefined;
  if (!isNode(node) || !node.range) throw new Error(`no node for ${key}`);
  return text.slice(node.range[0], node.range[1]);
}

// The values are those that the server reads from the same text.
describe('parseAllDocumentsAsServer', () => {
  it.each([
    ['a closing quote', "a: 'x\n\n'\nb: y\n", 'x\n'],
    ['a line of a single-quoted scalar', "a: 'x\ny'\nb: y\n", 'x y'],
    ['a line of a double-quoted scalar', 'a: "x\\t\ny"\nb: y\n', 'x\t y'],
    ['an escaped quote', "a: 'it''s\nhere'\nb: y\n", "it's here"],
  ])('reads %s at column 0', (_, text, value) => {
    const [doc] = parseAllDocumentsAsServer(text);
    expect(doc!.errors).toEqual([]);
    expect(doc!.toJS()).toEqual({ a: value, b: 'y' });
  });

  it('reads several such scalars', () => {
    const text = 'steps:\n- run: \'a\n\'\n- run: "b\nc"\n- run: d\n';
    const [doc] = parseAllDocumentsAsServer(text);
    expect(doc!.toJS()).toEqual({
      steps: [{ run: 'a ' }, { run: 'b c' }, { run: 'd' }],
    });
  });

  it('gives ranges that are offsets of the text', () => {
    const text = 'a: \'x\ny\'\nb: y\nc: "p\n\nq"\n---\nd: e\n';
    const docs = parseAllDocumentsAsServer(text);
    expect(valueText(docs[0]!, text, 'a')).toBe("'x\ny'");
    expect(valueText(docs[0]!, text, 'b')).toBe('y');
    expect(valueText(docs[0]!, text, 'c')).toBe('"p\n\nq"');
    expect(valueText(docs[1]!, text, 'd')).toBe('e');
    expect(text.slice(docs[1]!.range[0])).toBe('---\nd: e\n');
  });

  it('keeps the lines of a block scalar', () => {
    const text = "a: |\n  'x\n  y'\nb: 'p\nq'\n";
    const [doc] = parseAllDocumentsAsServer(text);
    expect(doc!.toJS()).toEqual({ a: "'x\ny'\n", b: 'p q' });
  });

  // A block scalar at column 0 can start a line with a quote: its lines must
  // not be indented as those of a quoted scalar.
  it.each([
    ['no closing quote', "a: 'x\nb: y\n"],
    ['a document marker', "a: 'x\n---\n'\n"],
    ['another error', "a: 'x\n'\nb: [y\n"],
    ['a block scalar line after it', "b: 'p\nq'\n--- |\n'x\ny'"],
  ])('keeps the errors of a quoted scalar with %s', (_, text) => {
    const docs = parseAllDocumentsAsServer(text);
    expect(docs.some((doc) => doc.errors.length > 0)).toBe(true);
  });
});

describe('parseDocumentAsServer', () => {
  it('reads a step at column 0 and ranges it in the text', () => {
    const text = "id: owner\nrun: 'echo x\n\n'\n";
    const doc = parseDocumentAsServer(text);
    expect(doc.toJS()).toEqual({ id: 'owner', run: 'echo x\n' });
    expect(valueText(doc, text, 'run')).toBe("'echo x\n\n'");
  });
});
