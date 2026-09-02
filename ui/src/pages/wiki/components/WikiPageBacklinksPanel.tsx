// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { workspaceWikiQueryForWorkspace } from '@/lib/workspace';
import { ChevronDown, ChevronRight, Link2 } from 'lucide-react';
import React, { useContext, useMemo, useState } from 'react';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  wikiPagePath: string | null;
  workspace: string | null;
  onSelectWikiPage: (
    wikiPagePath: string,
    title: string,
    workspace?: string | null
  ) => void;
};

function WikiPageBacklinksPanel({ wikiPagePath, workspace, onSelectWikiPage }: Props) {
  const [collapsed, setCollapsed] = useState(false);
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceWikiQueryForWorkspace(workspace),
    [workspace]
  );

  const { data } = useQuery(
    '/wiki/backlinks',
    wikiPagePath
      ? {
          params: {
            query: { remoteNode, target: wikiPagePath, ...workspaceQuery },
          },
        }
      : null
  );

  const items = data?.items ?? [];
  if (!wikiPagePath || items.length === 0) return null;

  return (
    <div className="border-t border-border shrink-0">
      <button
        type="button"
        onClick={() => setCollapsed((c) => !c)}
        aria-expanded={!collapsed}
        className="w-full flex items-center gap-1 px-3 py-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground hover:text-foreground"
      >
        {collapsed ? (
          <ChevronRight className="h-3 w-3" />
        ) : (
          <ChevronDown className="h-3 w-3" />
        )}
        <Link2 className="h-3 w-3" />
        <I18nText text={"Backlinks"} />
        <span className="ml-auto text-[10px] font-normal normal-case">
          {items.length}
        </span>
      </button>
      {!collapsed && (
        <div className="pb-1.5 max-h-40 overflow-y-auto">
          {items.map((item) => (
            <button
              key={`${item.workspace ?? ''}/${item.id}`}
              type="button"
              onClick={() =>
                onSelectWikiPage(item.id, item.title, item.workspace ?? null)
              }
              className="w-full text-left px-3 py-0.5 text-xs text-muted-foreground hover:text-foreground hover:bg-accent truncate"
              title={item.id}
            >
              {item.title}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export default WikiPageBacklinksPanel;
