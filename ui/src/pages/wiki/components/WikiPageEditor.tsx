// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import MarkdownEditor, {
  type MarkdownEditorInstance,
} from '@/components/editors/MarkdownEditor';
import { WikiPageMarkdownPreview } from '@/components/ui/wiki-page-markdown-preview';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useCanWrite, useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { useWikiPageTabContext } from '@/contexts/WikiPageTabContext';
import { useClient, useQuery } from '@/hooks/api';
import { useContentEditor } from '@/hooks/useContentEditor';
import { useWikiPageSSE } from '@/hooks/useWikiPageSSE';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import {
  isMutableWorkspaceSelection,
  workspaceWikiQueryForWorkspace,
  workspaceWikiSelectionQuery,
} from '@/lib/workspace';
import { cn } from '@/lib/utils';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  Check,
  ClipboardCopy,
  Copy,
  FileText,
  History,
  Save,
  Trash2,
  Undo2,
} from 'lucide-react';
import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { WikiLiveProvider } from '@/components/wiki-live/WikiLiveProvider';
import WikiPageExternalChangeDialog from './WikiPageExternalChangeDialog';
import { WikiPageHistoryModal } from './WikiPageHistoryModal';
import { WIKI_SSE_FALLBACK_INTERVAL_MS } from '../lib/wiki-page-polling';
import { useWikiPageDraftPersistence } from '../hooks/useWikiPageDraftPersistence';
import { attachmentUploadName } from '../lib/wiki-page-attachments';
import {
  readMigratedLocalStorage,
  writeLocalStorage,
} from '@/lib/local-storage-migration';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type Props = {
  tabId: string;
  wikiPagePath: string;
  workspace?: string | null;
  onDeleteWikiPage?: () => void;
  onContentChange?: (content: string | null) => void;
};

function normalizeWikiPageContentForSave(content: string): string {
  return content.replace(/\r\n/g, '\n').replace(/\n+$/, '');
}

function WikiPageEditor({
  tabId,
  wikiPagePath,
  workspace,
  onDeleteWikiPage,
  onContentChange,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspaceQuery = useMemo(
    () => workspaceWikiSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceQuery = useMemo(
    () =>
      workspace === undefined
        ? (selectedWorkspaceQuery ?? workspaceWikiQueryForWorkspace(null))
        : workspaceWikiQueryForWorkspace(workspace),
    [selectedWorkspaceQuery, workspace]
  );
  const workspaceTargetQuery = useMemo(
    () =>
      workspace === undefined
        ? selectedWorkspaceQuery
        : workspaceWikiQueryForWorkspace(workspace),
    [selectedWorkspaceQuery, workspace]
  );
  const workspaceQueryKey = useMemo(
    () => JSON.stringify(workspaceQuery),
    [workspaceQuery]
  );
  const canWriteSelectedScope = useCanWrite();
  const canWriteWikiWorkspace = useCanWriteForWorkspace(workspace ?? '');
  const canWrite =
    workspace === undefined ? canWriteSelectedScope : canWriteWikiWorkspace;
  const canEdit =
    canWrite &&
    !!workspaceTargetQuery &&
    (workspace !== undefined ||
      isMutableWorkspaceSelection(workspaceSelection));
  const canEditRef = useRef(canEdit);
  canEditRef.current = canEdit;
  const { showToast } = useSimpleToast();
  const { markTabUnsaved, markTabSaved, openWikiPage } =
    useWikiPageTabContext();

  const wikiPageSSE = useWikiPageSSE(
    wikiPagePath,
    !!wikiPagePath,
    workspaceQuery,
    remoteNode
  );

  // Fetch page — SWR is the single source of truth, refreshed by live invalidations
  const { data: page, mutate: mutateWikiPage } = useQuery(
    '/wiki/page',
    {
      params: {
        query: {
          remoteNode,
          path: wikiPagePath,
          ...workspaceQuery,
        },
      },
    },
    sseFallbackOptions(wikiPageSSE, WIKI_SSE_FALLBACK_INTERVAL_MS)
  );
  useSSECacheSync(wikiPageSSE, mutateWikiPage);
  const serverContent = page?.content ?? null;

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
      wikiPagePath,
      remoteNode,
      workspace: workspaceQueryKey,
    }),
    serverContent,
  });

  const [mode, setMode] = useState<'edit' | 'preview'>(() => {
    const stored = readMigratedLocalStorage(
      'wiki-page-editor-mode',
      'doc-editor-mode'
    );
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
    useWikiPageDraftPersistence({
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
    writeLocalStorage('wiki-page-editor-mode', mode);
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
    const contentToSave = normalizeWikiPageContentForSave(valueAtSaveStart);
    beginSave(contentToSave);
    try {
      const { error } = await client.PATCH('/wiki/page', {
        params: {
          query: { remoteNode, path: wikiPagePath, ...workspaceTargetQuery },
        },
        body: { content: contentToSave },
      });
      if (error) {
        cancelSave();
        showToast('Failed to save Wiki page');
      } else {
        const hasNewerEdits = currentValueRef.current !== valueAtSaveStart;
        if (!hasNewerEdits) {
          setCurrentValue(contentToSave);
        }
        markAsSaved(contentToSave);
        // Revalidate SWR cache from server as safety net
        mutateWikiPage();
        if (!hasNewerEdits) {
          markTabSaved(tabId);
          clearPersistedDraft();
        }
        showToast('Wiki page saved');
      }
    } catch {
      cancelSave();
      showToast('Failed to save Wiki page');
    } finally {
      setIsSaving(false);
    }
  }, [
    isSaving,
    canEdit,
    workspaceTargetQuery,
    client,
    remoteNode,
    wikiPagePath,
    beginSave,
    cancelSave,
    setCurrentValue,
    markAsSaved,
    mutateWikiPage,
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
  const [historyOpen, setHistoryOpen] = useState(false);

  // Attachment upload via paste/drop into the editor.
  const editorInstanceRef = useRef<MarkdownEditorInstance | null>(null);

  const uploadAttachment = useCallback(
    async (file: File) => {
      try {
        const name = attachmentUploadName(file);

        const { data, error } = await client.PUT('/wiki/page/attachment', {
          params: {
            query: {
              remoteNode,
              path: wikiPagePath,
              name,
              ...workspaceTargetQuery,
            },
          },
          body: file as unknown as string,
          bodySerializer: (body: unknown) => body as BodyInit,
          headers: { 'Content-Type': 'application/octet-stream' },
        });
        if (error || !data) {
          showToast(error?.message || 'Failed to upload attachment');
          return;
        }
        const isImage = file.type.startsWith('image/');
        // Names may contain spaces; the destination must be percent-encoded to
        // stay a valid Markdown link. The preview decodes it on resolution.
        const markdown = `${isImage ? '!' : ''}[${data.name}](attachment:${encodeURIComponent(data.name)})`;
        const editor = editorInstanceRef.current;
        const selection = editor?.getSelection();
        if (editor && selection) {
          editor.executeEdits('page-attachment', [
            { range: selection, text: markdown, forceMoveMarkers: true },
          ]);
          editor.focus();
        } else {
          setCurrentValue(`${currentValueRef.current ?? ''}\n${markdown}\n`);
        }
        showToast(`Attached ${data.name}`);
      } catch {
        showToast('Failed to upload attachment');
      }
    },
    [
      client,
      remoteNode,
      wikiPagePath,
      workspaceTargetQuery,
      showToast,
      setCurrentValue,
      currentValueRef,
    ]
  );
  const uploadAttachmentRef = useRef(uploadAttachment);
  uploadAttachmentRef.current = uploadAttachment;

  const handleEditorMount = useCallback((editor: MarkdownEditorInstance) => {
    editorInstanceRef.current = editor;
    const dom = editor.getContainerDomNode();
    const uploadFiles = (files: FileList | null | undefined, event: Event) => {
      if (!canEditRef.current || !files?.length) return;
      event.preventDefault();
      event.stopPropagation();
      void (async () => {
        for (const file of Array.from(files)) {
          await uploadAttachmentRef.current(file);
        }
      })();
    };
    const onPaste = (e: ClipboardEvent) => {
      uploadFiles(e.clipboardData?.files, e);
    };
    const onDrop = (e: DragEvent) => {
      uploadFiles(e.dataTransfer?.files, e);
    };
    const onDragOver = (e: DragEvent) => {
      if ((e.dataTransfer?.types ?? []).includes('Files')) {
        e.preventDefault();
      }
    };
    dom.addEventListener('paste', onPaste, true);
    dom.addEventListener('drop', onDrop, true);
    dom.addEventListener('dragover', onDragOver, true);
    editor.onDidDispose(() => {
      dom.removeEventListener('paste', onPaste, true);
      dom.removeEventListener('drop', onDrop, true);
      dom.removeEventListener('dragover', onDragOver, true);
      if (editorInstanceRef.current === editor) {
        editorInstanceRef.current = null;
      }
    });
  }, []);

  const title = page?.title || wikiPagePath.split('/').pop() || wikiPagePath;

  return (
    <div className="flex flex-col h-full">
      {/* Header bar */}
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-border bg-background shrink-0">
        <FileText className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="text-sm font-medium truncate">{title}</span>
        {hasUnsavedChanges && (
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0" />
        )}
        {page?.tags && page.tags.length > 0 && (
          <span className="hidden sm:flex items-center gap-1 shrink-0">
            {page.tags.map((tag) => (
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
            title={
              nameCopied
                ? ts('Name copied')
                : ts('Copy name: {name}', { name: title })
            }
            aria-label={ts(nameCopied ? 'Name copied' : 'Copy name')}
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

        {/* History */}
        <I18nProps>
          <button
            type="button"
            onClick={() => setHistoryOpen(true)}
            className="flex items-center gap-1 px-2 py-0.5 text-xs rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-all"
            title="Revision history"
          >
            <History className="h-3 w-3" />
            <span>
              <I18nText text={'History'} />
            </span>
          </button>
        </I18nProps>

        {/* Copy content */}
        <I18nProps>
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
            <span>
              <I18nText text={'Copy'} />
            </span>
          </button>
        </I18nProps>

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
            <I18nText text={'Edit'} />
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
            <I18nText text={'Preview'} />
          </button>
        </div>

        {/* Discard button */}
        {canEdit && hasUnsavedChanges && (
          <I18nProps>
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
              <I18nText text={'Discard'} />
            </button>
          </I18nProps>
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
            {isSaving ? (
              <I18nText text={'Saving...'} />
            ) : (
              <I18nText text={'Save'} />
            )}
          </button>
        )}
        {canEdit && onDeleteWikiPage && (
          <I18nProps>
            <button
              type="button"
              onClick={onDeleteWikiPage}
              className="flex items-center gap-1 px-2 py-1 text-xs rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
              title="Delete Wiki page"
              aria-label="Delete Wiki page"
            >
              <Trash2 className="h-3 w-3" />
            </button>
          </I18nProps>
        )}
      </div>

      {/* Editor / Preview */}
      <div className="flex-1 overflow-hidden min-h-0">
        {mode === 'edit' ? (
          <MarkdownEditor
            value={currentValue ?? ''}
            onChange={(val) => setCurrentValue(val ?? '')}
            readOnly={!canEdit}
            onEditorMount={handleEditorMount}
          />
        ) : (
          <div className="h-full overflow-y-auto p-6">
            <WikiLiveProvider workspace={workspace ?? null}>
              <WikiPageMarkdownPreview
                content={currentValue}
                linkContext={{
                  workspace: workspace ?? null,
                  wikiPagePath,
                  onOpenWikiPage: (path, ws) =>
                    openWikiPage(path, path.split('/').pop() || path, ws),
                }}
              />
            </WikiLiveProvider>
          </div>
        )}
      </div>

      {/* Conflict dialog */}
      <WikiPageExternalChangeDialog
        visible={conflict.hasConflict}
        onDiscard={() => {
          resolveConflict('discard');
          clearPersistedDraft();
          markTabSaved(tabId);
        }}
        onIgnore={() => resolveConflict('ignore')}
      />

      {/* Revision history */}
      <WikiPageHistoryModal
        isOpen={historyOpen}
        onClose={() => setHistoryOpen(false)}
        wikiPagePath={wikiPagePath}
        workspace={workspace ?? null}
        currentContent={currentValue ?? ''}
        onRestore={(content) => setCurrentValue(content)}
      />
    </div>
  );
}

export default WikiPageEditor;
