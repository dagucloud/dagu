// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useCanWrite } from '@/contexts/AuthContext';
import { ViewEditorDialog } from '@/features/views/ViewEditorDialog';
import { ViewPanel } from '@/features/views/ViewPanel';
import { useViews } from '@/hooks/useViews';
import { Pencil } from 'lucide-react';

/**
 * Standalone page for a single saved view (reached from a sidebar pin or a
 * /views/:viewId link). Unlike the Overview tab, it shows no Timeline/Cockpit
 * tabs — just the view's board, full-height and scrollable.
 */
export default function ViewPage(): React.ReactElement | null {
  const { viewId } = useParams();
  const navigate = useNavigate();
  const { views, isLoading } = useViews();
  const canWrite = useCanWrite();
  const [editorOpen, setEditorOpen] = useState(false);

  const view = views.find((v) => v.id === viewId);

  // Once views have loaded, redirect home if the requested view is gone.
  useEffect(() => {
    if (!isLoading && viewId && !view) {
      navigate('/');
    }
  }, [isLoading, viewId, view, navigate]);

  if (!view) {
    return null;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between pb-3">
        <h1 className="truncate text-lg font-semibold text-foreground">
          {view.name}
        </h1>
        {canWrite && (
          <button
            type="button"
            onClick={() => setEditorOpen(true)}
            className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground"
          >
            <Pencil className="h-3 w-3" /> Edit
          </button>
        )}
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <ViewPanel view={view} />
      </div>
      <ViewEditorDialog
        open={editorOpen}
        onOpenChange={setEditorOpen}
        view={view}
        onDeleted={() => navigate('/')}
      />
    </div>
  );
}
