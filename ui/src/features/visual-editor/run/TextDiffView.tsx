// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, type ReactElement } from 'react';
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import { useResolvedTheme } from '@/contexts/UserPreference';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { diffViewerStyles } from './diffStyles';
import { firstChange } from './lineDiffStats';

export interface TextDiffViewProps {
  original: string;
  modified: string;
  originalTitle: string;
  modifiedTitle: string;
  /** Side by side when true, one column when false. */
  split: boolean;
  /** Classes of the scroll container. */
  className?: string;
}

/**
 * The line diff of two texts in a scroll container that opens at the first
 * change. Equal texts show "No changes".
 */
export function TextDiffView({
  original,
  modified,
  originalTitle,
  modifiedTitle,
  split,
  className,
}: TextDiffViewProps): ReactElement {
  const isDark = useResolvedTheme() === 'dark';
  // A change far down a long file shows without a scroll: the view starts
  // a few lines above the first changed line.
  const change = useMemo(
    () => firstChange(original, modified),
    [original, modified]
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
    <div
      ref={scrollToChange}
      className={cn('min-h-0 flex-1 overflow-auto', className)}
    >
      {original === modified ? (
        <div className="p-3 text-sm text-muted-foreground">
          <I18nText text={'No changes'} />
        </div>
      ) : (
        <ReactDiffViewer
          oldValue={original}
          newValue={modified}
          splitView={split}
          leftTitle={originalTitle}
          rightTitle={modifiedTitle}
          useDarkTheme={isDark}
          compareMethod={DiffMethod.LINES}
          showDiffOnly={false}
          styles={diffViewerStyles}
        />
      )}
    </div>
  );
}
