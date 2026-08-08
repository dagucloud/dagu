// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import MarkdownEditor from '@/components/editors/MarkdownEditor';
import { DocMarkdownPreview } from '@/components/ui/doc-markdown-preview';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useCanWrite, useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { useDocTabContext } from '@/contexts/DocTabContext';
import { useClient, useQuery } from '@/hooks/api';
import { useContentEditor } from '@/hooks/useContentEditor';
import { useDocSSE } from '@/hooks/useDocSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import {
  isMutableWorkspaceSelection,
  workspaceDocumentQueryForWorkspace,
  workspaceDocumentSelectionQuery,
} from '@/lib/workspace';
import { cn } from '@/lib/utils';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  Check,
  ClipboardCopy,
  Copy,
  FileText,
  Save,
  Trash2,
  Undo2,
} from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import DocExternalChangeDialog from './DocExternalChangeDialog';
import { DOC_SSE_FALLBACK_INTERVAL_MS } from '../lib/doc-polling';
import { useDocDraftPersistence } from '../hooks/useDocDraftPersistence';

type Props = {
  tabId: string;
  docPath: string;
  workspace?: string | null;
  onDeleteDoc?: () => void;
  onContentChange?: (content: string | null) => void;
};

function normalizeDocContentForSave(content: string): string {
  return content.replace(/\r\n/g, '\n').replace(/\n+$/, '');
}

function DocEditor({
  tabId,
  docPath,
  workspace,
  onDeleteDoc,
  onContentChange,
}: Props) {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspaceQuery = useMemo(
    () => workspaceDocumentSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceQuery = useMemo(
    () =>
      workspace === undefined
        ? (selectedWorkspaceQuery ?? workspaceDocumentQueryForWorkspace(null))
        : workspaceDocumentQueryForWorkspace(workspace),
    [selectedWorkspaceQuery, workspace]
  );
  const workspaceTargetQuery = useMemo(
    () =>
      workspace === undefined
        ? selectedWorkspaceQuery
        : workspaceDocumentQueryForWorkspace(workspace),
    [selectedWorkspaceQuery, workspace]
  );
  const workspaceQueryKey = useMemo(
    () => JSON.stringify(workspaceQuery),
    [workspaceQuery]
  );
  const canWriteSelectedScope = useCanWrite();
  const canWriteDocWorkspace = useCanWriteForWorkspace(workspace ?? '');
  const canWrite =
    workspace === undefined ? canWriteSelectedScope : canWriteDocWorkspace;
  const canEdit =
    canWrite &&
    !!workspaceTargetQuery &&
    (workspace !== undefined ||
      isMutableWorkspaceSelection(workspaceSelection));
  const canEditRef = useRef(canEdit);
  canEditRef.current = canEdit;
  const { showToast } = useSimpleToast();
  const { markTabUnsaved, markTabSaved } = useDocTabContext();

  const docSSE = useDocSSE(docPath, !!docPath, workspaceQuery, remoteNode);

  // Fetch doc — SWR is the single source of truth, refreshed by live invalidations
  const { data: doc, mutate: mutateDoc } = useQuery(
    '/docs/doc',
    {
      params: {
        query: {
          remoteNode,
          path: docPath,
          ...workspaceQuery,
        },
      },
    },
    sseFallbackOptions(docSSE, DOC_SSE_FALLBACK_INTERVAL_MS)
  );
  useSSECacheSync(docSSE, mutateDoc);
  const serverContent = doc?.content ?? null;

  // Change tracking (source-agnostic)
  const {
    currentValue,
    setCurrentValue,
    hasUnsavedChanges,
    conflict,
    resolveConflict,
    beginSave,
    cancelSave,
    markAsSaved,
    discardChanges,
  } = useContentEditor({
    key: JSON.stringify({
      docPath,
      remoteNode,
      workspace: workspaceQueryKey,
    }),
    serverContent,
  });

  const [mode, setMode] = useState<'edit' | 'preview'>(() => {
    const stored = localStorage.getItem('doc-editor-mode');
    return stored === 'preview' ? 'preview' : 'edit';
  });
  const [isSaving, setIsSaving] = useState(false);

  const scopedDraftKey = useMemo(
    () =>
      JSON.stringify({
        tabId,
        remoteNode,
        workspace: workspaceQueryKey,
      }),
    [remoteNode, tabId, workspaceQueryKey]
  );
  const { clearPersistedDraft, currentValueRef, hasUnsavedChangesRef } =
    useDocDraftPersistence({
      draftKey: scopedDraftKey,
      currentValue,
      hasUnsavedChanges,
      setCurrentValue,
    });

  // Sync unsaved state to tab context
  useEffect(() => {
    if (hasUnsavedChanges) {
      markTabUnsaved(tabId);
    } else {
      markTabSaved(tabId);
    }
  }, [hasUnsavedChanges, tabId, markTabUnsaved, markTabSaved]);

  // Persist mode preference
  useEffect(() => {
    localStorage.setItem('doc-editor-mode', mode);
  }, [mode]);

  // Report content changes to parent for outline panel (debounced)
  const contentChangeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null
  );
  useEffect(() => {
    if (contentChangeTimerRef.current) {
      clearTimeout(contentChangeTimerRef.current);
    }
    contentChangeTimerRef.current = setTimeout(() => {
      onContentChange?.(currentValue);
    }, 300);
    return () => {
      if (contentChangeTimerRef.current)
        clearTimeout(contentChangeTimerRef.current);
    };
  }, [currentValue, onContentChange]);

  const handleSave = useCallback(async () => {
    if (
      isSaving ||
      !canEdit ||
      !workspaceTargetQuery ||
      !hasUnsavedChangesRef.current
    ) {
      return;
    }
    setIsSaving(true);
    const valueAtSaveStart = currentValueRef.current ?? '';
    const contentToSave = normalizeDocContentForSave(valueAtSaveStart);
    beginSave(contentToSave);
    try {
      const { error } = await client.PATCH('/docs/doc', {
        params: {
          query: { remoteNode, path: docPath, ...workspaceTargetQuery },
        },
        body: { content: contentToSave },
      });
      if (error) {
        cancelSave();
        showToast('Failed to save document');
      } else {
        const hasNewerEdits = currentValueRef.current !== valueAtSaveStart;
        if (!hasNewerEdits) {
          setCurrentValue(contentToSave);
        }
        markAsSaved(contentToSave);
        // Revalidate SWR cache from server as safety net
        mutateDoc();
        if (!hasNewerEdits) {
          markTabSaved(tabId);
          clearPersistedDraft();
        }
        showToast('Document saved');
      }
    } catch {
      cancelSave();
      showToast('Failed to save document');
    } finally {
      setIsSaving(false);
    }
  }, [
    isSaving,
    canEdit,
    workspaceTargetQuery,
    client,
    remoteNode,
    docPath,
    beginSave,
    cancelSave,
    setCurrentValue,
    markAsSaved,
    mutateDoc,
    markTabSaved,
    clearPersistedDraft,
    tabId,
    showToast,
  ]);

  // Keep save handler in ref for keyboard shortcut
  const handleSaveRef = useRef(handleSave);
  handleSaveRef.current = handleSave;

  // Ctrl+S / Cmd+S
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (
        (e.ctrlKey || e.metaKey) &&
        e.key.toLowerCase() === 's' &&
        canEditRef.current
      ) {
        e.preventDefault();
        handleSaveRef.current();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, []);

  const { copied: nameCopied, copy: copyName } = useCopyFeedback();
  const { copied: copiedContent, copy: copyContent } = useCopyFeedback();

  const title = doc?.title || docPath.split('/').pop() || docPath;

  return (
    <div className="flex flex-col h-full">
      {/* Header bar */}
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-border bg-background shrink-0">
        <FileText className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="text-sm font-medium truncate">{title}</span>
        {hasUnsavedChanges && (
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0" />
        )}
        {doc?.tags && doc.tags.length > 0 && (
          <span className="hidden sm:flex items-center gap-1 shrink-0">
            {doc.tags.map((tag) => (
              <span
                key={tag}
                className="px-1.5 py-0.5 text-[10px] leading-none rounded-full bg-muted text-muted-foreground border border-border"
              >
                {tag}
              </span>
            ))}
          </span>
        )}

        {title && (
          <button
            type="button"
            onClick={() => copyName(title)}
            className="inline-flex items-center gap-1 px-1.5 py-0.5 text-xs rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-all shrink-0"
            title={nameCopied ? 'Name copied' : `Copy name: ${title}`}
            aria-label={nameCopied ? 'Name copied' : 'Copy name'}
          >
            {nameCopied ? (
              <Check className="h-3.5 w-3.5 text-green-500" />
            ) : (
              <Copy className="h-3.5 w-3.5" />
            )}
          </button>
        )}
        <span className="sr-only" aria-live="polite">
          {nameCopied ? `Copied name ${title}` : ''}
        </span>

        <div className="flex-1" />

        {/* Copy content */}
        <button
          type="button"
          onClick={() => copyContent(currentValue ?? '')}
          disabled={!currentValue}
          className="flex items-center gap-1 px-2 py-0.5 text-xs rounded-md text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed transition-all"
          title="Copy content"
        >
          {copiedContent ? (
            <Check className="h-3 w-3 text-green-500" />
          ) : (
            <ClipboardCopy className="h-3 w-3" />
          )}
          <span>Copy</span>
        </button>

        {/* Mode toggle */}
        <div className="flex rounded-md border border-border overflow-hidden">
          <button
            type="button"
            className={cn(
              'px-2 py-0.5 text-xs transition-colors',
              mode === 'edit'
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground hover:text-foreground'
            )}
            onClick={() => setMode('edit')}
            aria-pressed={mode === 'edit'}
          >
            Edit
          </button>
          <button
            type="button"
            className={cn(
              'px-2 py-0.5 text-xs transition-colors',
              mode === 'preview'
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground hover:text-foreground'
            )}
            onClick={() => setMode('preview')}
            aria-pressed={mode === 'preview'}
          >
            Preview
          </button>
        </div>

        {/* Discard button */}
        {canEdit && hasUnsavedChanges && (
          <button
            type="button"
            onClick={() => {
              discardChanges();
              clearPersistedDraft();
              markTabSaved(tabId);
            }}
            className="flex items-center gap-1 px-2 py-1 text-xs rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
            title="Discard changes"
          >
            <Undo2 className="h-3 w-3" />
            Discard
          </button>
        )}

        {/* Save button */}
        {canEdit && (
          <button
            type="button"
            onClick={handleSave}
            disabled={!hasUnsavedChanges || isSaving}
            className={cn(
              'flex items-center gap-1 px-2 py-1 text-xs rounded-md transition-colors',
              hasUnsavedChanges
                ? 'bg-primary text-primary-foreground hover:bg-primary/90'
                : 'text-muted-foreground cursor-not-allowed'
            )}
          >
            <Save className="h-3 w-3" />
            {isSaving ? 'Saving...' : 'Save'}
          </button>
        )}
        {canEdit && onDeleteDoc && (
          <button
            type="button"
            onClick={onDeleteDoc}
            className="flex items-center gap-1 px-2 py-1 text-xs rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
            title="Delete document"
            aria-label="Delete document"
          >
            <Trash2 className="h-3 w-3" />
          </button>
        )}
      </div>

      {/* Editor / Preview */}
      <div className="flex-1 overflow-hidden min-h-0">
        {mode === 'edit' ? (
          <MarkdownEditor
            value={currentValue ?? ''}
            onChange={(val) => setCurrentValue(val ?? '')}
            readOnly={!canEdit}
          />
        ) : (
          <div className="h-full overflow-y-auto p-6">
            <DocMarkdownPreview content={currentValue} />
          </div>
        )}
      </div>

      {/* Conflict dialog */}
      <DocExternalChangeDialog
        visible={conflict.hasConflict}
        onDiscard={() => {
          resolveConflict('discard');
          clearPersistedDraft();
          markTabSaved(tabId);
        }}
        onIgnore={() => resolveConflict('ignore')}
      />
    </div>
  );
}

export default DocEditor;
