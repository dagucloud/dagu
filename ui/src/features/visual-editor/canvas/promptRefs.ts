// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { findReferenceTokens } from '../references/tokens';

// The nodes of the tree that the Markdown parser gives, as far as this file reads them.
interface MdNode {
  type: string;
  value?: string;
  children?: MdNode[];
  data?: { hName: string; hProperties: { className: string[] } };
}

// The colour of a reference in the editor: info for a reference, warning for an unsupported one.
const OK = ['font-medium', 'text-info'];
const BAD = ['font-medium', 'text-warning'];

function splitText(node: MdNode): MdNode[] {
  const text = node.value ?? '';
  const tokens = findReferenceTokens(text);
  if (tokens.length === 0) return [node];
  const parts: MdNode[] = [];
  let pos = 0;
  for (const token of tokens) {
    if (token.from > pos) {
      parts.push({ type: 'text', value: text.slice(pos, token.from) });
    }
    parts.push({
      type: 'emphasis',
      data: {
        hName: 'span',
        hProperties: { className: token.kind === 'unsupported' ? BAD : OK },
      },
      children: [{ type: 'text', value: token.text }],
    });
    pos = token.to;
  }
  if (pos < text.length) parts.push({ type: 'text', value: text.slice(pos) });
  return parts;
}

function walk(node: MdNode): void {
  if (!node.children) return;
  node.children = node.children.flatMap((child) => {
    if (child.type === 'text') return splitText(child);
    walk(child);
    return [child];
  });
}

/**
 * A Markdown plugin that gives each `${...}` reference of a text the colour that
 * the editor gives it. Code stays as it is: a code node holds no text node.
 */
export function remarkReferences() {
  return (tree: MdNode) => walk(tree);
}
