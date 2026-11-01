// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useContext, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Pencil } from 'lucide-react';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { ViewEditorDialog } from '@/features/views/ViewEditorDialog';
import { ViewPanel } from '@/features/views/ViewPanel';
import { PeekHost } from '@/features/peek/PeekHost';
import { useViews } from '@/hooks/useViews';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';

/**
 * Standalone page for a single saved view (reached from a sidebar pin or a
 * /views/:viewId link). Unlike the Overview tab, it shows no Timeline/Cockpit
 * tabs — just the view's board, full-height and scrollable.
 */
export default function ViewPage(): React.ReactElement | null {
  const { viewId } = useParams();
  const navigate = useNavigate();
  const { views, isLoading, error, refresh } = useViews();
  const { ts } = useI18n();
  const view = views.find((v) => v.id === viewId);
  const canWrite = useCanWriteForWorkspace(view?.workspace ?? '');
  const [editorOpen, setEditorOpen] = useState(false);
  const { setTitle } = useContext(AppBarContext);
  const viewTitle =
    view?.name ?? (isLoading || error ? '' : ts('View not found'));

  useEffect(() => {
    setTitle(viewTitle);
  }, [setTitle, viewTitle]);

  if (!view) {
    if (isLoading) {
      return <PageLoading />;
    }
    if (error) {
      return (
        <LoadErrorState
          title={ts("Couldn't load views")}
          error={error}
          onRetry={() => void refresh()}
        />
      );
    }
    return (
      <NotFoundState
        title={ts('View not found')}
        description={ts('This view may have been deleted.')}
        action={{ to: '/', label: ts('Go to Overview') }}
      />
    );
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
            <Pencil className="h-3 w-3" /> <I18nText text={'Edit'} />
          </button>
        )}
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <PeekHost>
          <ViewPanel view={view} />
        </PeekHost>
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
