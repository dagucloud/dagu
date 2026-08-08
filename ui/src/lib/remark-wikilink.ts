// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { visit } from 'unist-util-visit';

/**
 * URL scheme carried by link nodes produced from [[wikilink]] syntax.
 * Renderers dispatch on this prefix to resolve doc and dag targets.
 */
export const WIKILINK_SCHEME = 'wikilink:';

/** Prefix marking a wikilink target as a DAG reference: [[dag:name]]. */
export const WIKILINK_DAG_PREFIX = 'dag:';

const WIKILINK_PATTERN = /\[\[([^[\]|#]+)(#[^[\]|]*)?(\|[^[\]]*)?\]\]/g;

export type ParsedWikilink = {
  target: string;
  anchor: string;
  label: string;
};

/** Parse a wikilink: URL back into target and anchor. */
export function parseWikilinkHref(
  href: string
): { target: string; anchor: string } | null {
  if (!href.startsWith(WIKILINK_SCHEME)) return null;
  // The markdown pipeline percent-encodes URLs; decode to recover targets
  // containing spaces and human-readable anchors.
  let raw = href.slice(WIKILINK_SCHEME.length);
  try {
    raw = decodeURIComponent(raw);
  } catch {
    // Keep the raw value when it is not valid percent-encoding.
  }
  const hashIdx = raw.indexOf('#');
  if (hashIdx < 0) return { target: raw, anchor: '' };
  return { target: raw.slice(0, hashIdx), anchor: raw.slice(hashIdx + 1) };
}

function parseMatch(match: RegExpExecArray): ParsedWikilink | null {
  const target = match[1]?.trim() ?? '';
  if (!target) return null;
  return {
    target,
    anchor: (match[2] ?? '').replace(/^#/, '').trim(),
    label: (match[3] ?? '').replace(/^\|/, '').trim(),
  };
}

type MdNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MdNode[];
  data?: { hProperties?: Record<string, unknown> };
};

/**
 * remark plugin turning [[target]], [[target#anchor]], and [[target|label]]
 * text into link nodes with a wikilink: URL. Text inside code blocks and
 * inline code never appears as mdast text nodes, so it is left untouched.
 */
export function remarkWikilink() {
  return (tree: MdNode) => {
    visit(
      tree as never,
      'text',
      (node: MdNode, index: number | undefined, parent: MdNode | undefined) => {
        if (!parent || index === undefined || parent.type === 'link') return;
        const value = node.value ?? '';
        if (!value.includes('[[')) return;

        const replacement: MdNode[] = [];
        let last = 0;
        WIKILINK_PATTERN.lastIndex = 0;
        let match: RegExpExecArray | null;
        while ((match = WIKILINK_PATTERN.exec(value)) !== null) {
          const link = parseMatch(match);
          if (!link) continue;
          if (match.index > last) {
            replacement.push({
              type: 'text',
              value: value.slice(last, match.index),
            });
          }
          replacement.push({
            type: 'link',
            url:
              WIKILINK_SCHEME +
              link.target +
              (link.anchor ? `#${link.anchor}` : ''),
            data: { hProperties: { className: 'wikilink' } },
            children: [{ type: 'text', value: link.label || link.target }],
          });
          last = match.index + match[0].length;
        }
        if (replacement.length === 0) return;
        if (last < value.length) {
          replacement.push({ type: 'text', value: value.slice(last) });
        }
        parent.children?.splice(index, 1, ...replacement);
        return index + replacement.length;
      }
    );
  };
}
