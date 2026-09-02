// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { KILN_DARK, KILN_LIGHT } from '@/lib/monaco-theme';
import { workspaceWikiQueryForWorkspace } from '@/lib/workspace';
import { DiffEditor } from '@monaco-editor/react';
import { History, RotateCcw } from 'lucide-react';
import { useContext, useMemo, useRef, useState } from 'react';
import { I18nText } from '@/i18n/I18nText';

type WikiPageRevision = components['schemas']['WikiPageRevisionResponse'];

type Props = {
  isOpen: boolean;
  onClose: () => void;
  wikiPagePath: string;
  workspace: string | null;
  currentContent: string;
  onRestore: (content: string) => void;
};

function formatSavedAt(savedAt: string): string {
  const date = new Date(savedAt);
  return Number.isNaN(date.getTime()) ? savedAt : date.toLocaleString();
}

export function WikiPageHistoryModal({
  isOpen,
  onClose,
  wikiPagePath,
  workspace,
  currentContent,
  onRestore,
}: Props) {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceWikiQueryForWorkspace(workspace),
    [workspace]
  );

  const [selectedRev, setSelectedRev] = useState<string | null>(null);
  const selectedRevRef = useRef<string | null>(null);
  const [revisionContent, setRevisionContent] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const { data, isLoading } = useQuery(
    '/wiki/page/revisions',
    isOpen
      ? {
          params: {
            query: { remoteNode, path: wikiPagePath, ...workspaceQuery },
          },
        }
      : null
  );
  const revisions: WikiPageRevision[] = data?.revisions ?? [];

  const selectRevision = async (rev: string) => {
    setSelectedRev(rev);
    selectedRevRef.current = rev;
    setRevisionContent(null);
    setLoadError(null);
    try {
      const { data: revData, error } = await client.GET('/wiki/page/revision', {
        params: {
          query: { remoteNode, path: wikiPagePath, rev, ...workspaceQuery },
        },
      });
      if (selectedRevRef.current !== rev) return;
      if (error || !revData) {
        setLoadError(error?.message || 'Failed to load revision');
        return;
      }
      setRevisionContent(revData.content ?? '');
    } catch (error) {
      if (selectedRevRef.current !== rev) return;
      setLoadError(
        error instanceof Error ? error.message : 'Failed to load revision'
      );
    }
  };

  const handleClose = () => {
    setSelectedRev(null);
    selectedRevRef.current = null;
    setRevisionContent(null);
    setLoadError(null);
    onClose();
  };

  const isDarkMode =
    typeof window !== 'undefined' &&
    document.documentElement.classList.contains('dark');

  return (
    <Dialog open={isOpen} onOpenChange={(open) => !open && handleClose()}>
      <DialogContent className="sm:max-w-[900px] max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <History className="h-4 w-4" />
            <I18nText text={"History:"} /> {wikiPagePath}
          </DialogTitle>
          <DialogDescription>
            <I18nText text={"Stored versions of this Wiki page, newest first. Loading a revision into the editor marks it unsaved; save to restore it."} />
          </DialogDescription>
        </DialogHeader>
        {revisions.length === 0 ? (
          <div className="text-sm text-muted-foreground py-6 text-center">
            {isLoading
              ? <I18nText text={"Loading revisions…"} />
              : <I18nText text={"No stored revisions yet. Revisions are captured on each save."} />}
          </div>
        ) : (
          <div className="flex gap-3 min-h-0 flex-1">
            <div className="w-48 shrink-0 overflow-y-auto border border-border rounded-md">
              {revisions.map((revision) => (
                <button
                  key={revision.rev}
                  type="button"
                  onClick={() => selectRevision(revision.rev)}
                  className={`w-full text-left px-2 py-1.5 text-xs border-b border-border last:border-b-0 hover:bg-accent ${
                    selectedRev === revision.rev ? 'bg-accent' : ''
                  }`}
                >
                  <div>{formatSavedAt(revision.savedAt)}</div>
                  <div className="text-muted-foreground">
                    {revision.size} <I18nText text={"bytes"} />
                  </div>
                </button>
              ))}
            </div>
            <div className="flex-1 min-w-0 flex flex-col gap-2">
              {loadError && (
                <div className="text-destructive text-sm">{loadError}</div>
              )}
              {revisionContent !== null ? (
                <>
                  <div className="flex-1 min-h-[300px] border border-border rounded-md overflow-hidden">
                    <DiffEditor
                      original={revisionContent}
                      modified={currentContent}
                      language="markdown"
                      theme={isDarkMode ? KILN_DARK : KILN_LIGHT}
                      options={{
                        readOnly: true,
                        renderSideBySide: true,
                        minimap: { enabled: false },
                        scrollBeyondLastLine: false,
                        fontSize: 12,
                      }}
                    />
                  </div>
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      size="sm"
                      onClick={() => {
                        onRestore(revisionContent);
                        handleClose();
                      }}
                    >
                      <RotateCcw className="h-3.5 w-3.5" />
                      <I18nText text={"Load into editor"} />
                    </Button>
                  </div>
                </>
              ) : (
                <div className="text-sm text-muted-foreground py-6 text-center">
                  {selectedRev ? <I18nText text={"Loading…"} /> : <I18nText text={"Select a revision to compare."} />}
                </div>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
