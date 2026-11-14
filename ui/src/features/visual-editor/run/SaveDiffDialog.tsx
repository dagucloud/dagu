// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Spinner } from '@/components/ui/spinner';
import { useMemo, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { lineDiffStats } from './lineDiffStats';
import { DiffLayoutToggle } from './DiffLayoutToggle';
import { TextDiffView } from './TextDiffView';

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
  const stats = useMemo(
    () => (p.open ? lineDiffStats(p.original, p.edited) : null),
    [p.open, p.original, p.edited]
  );
  const [split, setSplit] = useState(true);
  const unchanged = p.original === p.edited;

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
              <DiffLayoutToggle split={split} onSplitChange={setSplit} />
            )}
          </div>
        </DialogHeader>
        <TextDiffView
          original={p.original}
          modified={p.edited}
          originalTitle={ts('Saved version')}
          modifiedTitle={ts('Your changes')}
          split={split}
        />
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
