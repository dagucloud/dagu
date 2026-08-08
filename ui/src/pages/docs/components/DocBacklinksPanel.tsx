// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { workspaceDocumentQueryForWorkspace } from '@/lib/workspace';
import { ChevronDown, ChevronRight, Link2 } from 'lucide-react';
import React, { useContext, useMemo, useState } from 'react';

type Props = {
  docPath: string | null;
  workspace: string | null;
  onSelectDoc: (
    docPath: string,
    title: string,
    workspace?: string | null
  ) => void;
};

function DocBacklinksPanel({ docPath, workspace, onSelectDoc }: Props) {
  const [collapsed, setCollapsed] = useState(false);
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceDocumentQueryForWorkspace(workspace),
    [workspace]
  );

  const { data } = useQuery(
    '/docs/backlinks',
    docPath
      ? {
          params: {
            query: { remoteNode, target: docPath, ...workspaceQuery },
          },
        }
      : null
  );

  const items = data?.items ?? [];
  if (!docPath || items.length === 0) return null;

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
        Backlinks
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
                onSelectDoc(item.id, item.title, item.workspace ?? null)
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

export default DocBacklinksPanel;
