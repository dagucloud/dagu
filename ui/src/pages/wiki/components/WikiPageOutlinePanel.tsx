// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronRight, List } from 'lucide-react';
import React, { useEffect, useMemo, useState } from 'react';
import { readMigratedLocalStorage } from '@/lib/local-storage-migration';
import { slugifyHeading } from '@/lib/text-utils';
import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';

export interface OutlineHeading {
  level: number;
  text: string;
  anchor: string;
}

export function extractHeadings(
  markdown: string | null | undefined
): OutlineHeading[] {
  if (!markdown) return [];
  const headings: OutlineHeading[] = [];
  const lines = markdown.split('\n');
  let codeFence: string | null = null;

  for (const line of lines) {
    const trimmed = line.trimStart();
    const fenceMatch = trimmed.match(/^(```|~~~)/);
    if (fenceMatch) {
      const fence = fenceMatch[1] ?? '';
      if (!fence) continue;
      codeFence = codeFence === fence ? null : (codeFence ?? fence);
      continue;
    }
    if (codeFence) continue;

    const match = trimmed.match(/^(#{1,6})\s+(.+)$/);
    if (match && match[1] && match[2]) {
      const level = match[1].length;
      const text = match[2].trim();
      headings.push({ level, text, anchor: slugifyHeading(text) });
    }
  }
  return headings;
}

type Props = {
  markdown: string | null | undefined;
  onHeadingClick: (anchor: string) => void;
};

function WikiPageOutlinePanel({ markdown, onHeadingClick }: Props) {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return (
        readMigratedLocalStorage(
          'dagu_wiki_outline_collapsed',
          'dagu_doc_outline_collapsed'
        ) === 'true'
      );
    } catch {
      return false;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem('dagu_wiki_outline_collapsed', collapsed.toString());
    } catch {
      /* ignore */
    }
  }, [collapsed]);

  const headings = useMemo(() => extractHeadings(markdown), [markdown]);

  if (headings.length === 0) return null;

  return (
    <div className="border-t border-border">
      <button
        type="button"
        className="flex items-center gap-1.5 w-full px-3 py-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground hover:text-foreground"
        onClick={() => setCollapsed((c) => !c)}
        aria-expanded={!collapsed}
      >
        {collapsed ? (
          <ChevronRight className="h-3 w-3" />
        ) : (
          <ChevronDown className="h-3 w-3" />
        )}
        <List className="h-3 w-3" />
        <I18nText text={"Outline"} />
      </button>
      {!collapsed && (
        <div className="overflow-y-auto max-h-48 pb-1">
          {headings.map((h, i) => (
            <button
              key={`${h.anchor}-${i}`}
              type="button"
              className={cn(
                'block w-full text-left text-xs truncate py-0.5 px-3 hover:bg-accent/50 text-muted-foreground hover:text-foreground'
              )}
              style={{ paddingLeft: `${(h.level - 1) * 12 + 12}px` }}
              onClick={() => onHeadingClick(h.anchor)}
              title={h.text}
            >
              {h.text}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export default WikiPageOutlinePanel;
