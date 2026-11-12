// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { parseAllDocumentsAsServer } from '@/features/visual-editor/doc/serverYaml';

/** A part of a text, from offset `from` up to `to`. */
export interface TextSpan {
  from: number;
  to: number;
}

/**
 * The document of the child workflow `name` in the text of a file: from the
 * line after its `---` marker to the end of the document, with the comments
 * at its top. Null when no document after the first has that name.
 */
export function childDocumentSpan(text: string, name: string): TextSpan | null {
  const docs = parseAllDocumentsAsServer(text);
  for (const doc of docs.slice(1)) {
    if (doc.get('name') !== name) continue;
    const [start, , end] = doc.range;
    const lineEnd = text.indexOf('\n', start);
    const marked = text.startsWith('---', start) && lineEnd !== -1;
    return { from: marked ? lineEnd + 1 : start, to: end };
  }
  return null;
}

/** The text with `span` replaced by `part`. */
export function replaceSpan(
  text: string,
  span: TextSpan,
  part: string
): string {
  return text.slice(0, span.from) + part + text.slice(span.to);
}
