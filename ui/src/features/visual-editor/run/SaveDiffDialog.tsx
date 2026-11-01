// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Spinner } from '@/components/ui/spinner';
import { useCallback, useMemo, useState, type ReactElement } from 'react';
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useResolvedTheme } from '@/contexts/UserPreference';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { diffViewerStyles } from './diffStyles';
import { firstChange, lineDiffStats } from './lineDiffStats';

export interface SaveDiffDialogProps {
  open: boolean;
  original: string;
  edited: string;
  saving: boolean;
  onConfirm(): void;
  onCancel(): void;
}

export function SaveDiffDialog(p: SaveDiffDialogProps): ReactElement {
  const { ts } = useI18n();
  const isDark = useResolvedTheme() === 'dark';
  const stats = useMemo(
    () => (p.open ? lineDiffStats(p.original, p.edited) : null),
    [p.open, p.original, p.edited]
  );
  const [split, setSplit] = useState(true);
  const unchanged = p.original === p.edited;
  // A change far down a long file shows without a scroll: the view starts
  // a few lines above the first changed line.
  const change = useMemo(
    () => (p.open ? firstChange(p.original, p.edited) : null),
    [p.open, p.original, p.edited]
  );
  const scrollToChange = useCallback(
    (el: HTMLDivElement | null) => {
      if (!el || !change || change.lines === 0) return;
      const lineHeight = el.scrollHeight / (change.lines + 1);
      el.scrollTop = Math.max(0, (change.line - 3) * lineHeight);
    },
    [change]
  );

  return (
    <Dialog
      open={p.open}
      onOpenChange={(next) => {
        if (!next && !p.saving) p.onCancel();
      }}
    >
      <DialogContent
        hideCloseButton
        aria-describedby={undefined}
        className="max-w-5xl max-h-[85vh] overflow-hidden flex flex-col gap-0 p-0 duration-100"
      >
        <DialogHeader className="flex flex-row items-center justify-between space-y-0 border-b border-border/40 p-3">
          <DialogTitle className="text-sm">
            <I18nText text={'Review changes'} />
          </DialogTitle>
          <div className="flex items-center gap-2">
            {stats && !unchanged && (
              <span className="text-xs text-muted-foreground">
                {ts('{added} added, {removed} removed', {
                  added: stats.added,
                  removed: stats.removed,
                })}
              </span>
            )}
            {!unchanged && (
              <div className="flex gap-1">
                <Button
                  variant={split ? 'secondary' : 'ghost'}
                  size="xs"
                  aria-pressed={split}
                  onClick={() => setSplit(true)}
                >
                  <I18nText text={'Split'} />
                </Button>
                <Button
                  variant={split ? 'ghost' : 'secondary'}
                  size="xs"
                  aria-pressed={!split}
                  onClick={() => setSplit(false)}
                >
                  <I18nText text={'Unified'} />
                </Button>
              </div>
            )}
          </div>
        </DialogHeader>
        <div ref={scrollToChange} className="min-h-0 flex-1 overflow-auto">
          {unchanged ? (
            <div className="p-3 text-sm text-muted-foreground">
              <I18nText text={'No changes'} />
            </div>
          ) : (
            <ReactDiffViewer
              oldValue={p.original}
              newValue={p.edited}
              splitView={split}
              leftTitle={ts('Saved version')}
              rightTitle={ts('Your changes')}
              useDarkTheme={isDark}
              compareMethod={DiffMethod.LINES}
              showDiffOnly={false}
              styles={diffViewerStyles}
            />
          )}
        </div>
        <DialogFooter className="border-t border-border/40 p-3">
          <Button
            variant="ghost"
            size="xs"
            disabled={p.saving}
            onClick={p.onCancel}
          >
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            variant="primary"
            size="xs"
            disabled={p.saving}
            onClick={p.onConfirm}
          >
            {p.saving && <Spinner />}
            <I18nText text={'Save'} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
