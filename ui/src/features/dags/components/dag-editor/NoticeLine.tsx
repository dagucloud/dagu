// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * One line of notices over the canvas. It shows the notice with the highest
 * priority; a '+n' button opens the others in a popover.
 *
 * @module features/dags/components/dag-editor
 */
import { X } from 'lucide-react';
import React from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { WorkspaceNotice } from './hostTypes';
import { AnchoredPopover } from './IssuesPopover';

/** A host notice, or a notice of the editor with its own buttons. */
export interface NoticeLineItem extends WorkspaceNotice {
  /** A second button after the action, such as 'Discard'. */
  secondaryAction?: { label: React.ReactNode; run(): void };
  /** Called by the Dismiss button. Without it, the line calls dismiss and hides the notice. */
  onDismiss?(): void;
  /** 'alert' for a refused edit. The default is 'status'. */
  role?: 'status' | 'alert';
  /** Accessible name of the notice. */
  label?: string;
}

export interface NoticeLineProps {
  notices: NoticeLineItem[];
  className?: string;
}

const TONE: Record<WorkspaceNotice['tone'], string> = {
  info: 'border-border bg-muted/60 text-foreground',
  warning: 'border-warning/40 bg-warning/10 text-warning',
  error: 'border-destructive/40 bg-destructive/10 text-destructive',
};

/** Highest priority first; equal priorities keep their order. */
function byPriority(notices: NoticeLineItem[]): NoticeLineItem[] {
  return notices
    .map((notice, index) => ({ notice, index }))
    .sort((a, b) => b.notice.priority - a.notice.priority || a.index - b.index)
    .map(({ notice }) => notice);
}

export function NoticeLine({ notices, className }: NoticeLineProps) {
  const { ts } = useI18n();
  const [open, setOpen] = React.useState(false);
  const anchorRef = React.useRef<HTMLButtonElement>(null);
  const lineRef = React.useRef<HTMLDivElement>(null);
  const panelId = React.useId();
  // Ids that the line hid itself. An id leaves the set when its notice
  // leaves the list, so that a later notice with the same id shows again.
  const [hidden, setHidden] = React.useState<ReadonlySet<string>>(
    () => new Set()
  );

  const ids = notices.map((notice) => notice.id).join('\n');
  React.useEffect(() => {
    const present = new Set(ids.split('\n'));
    setHidden((previous) => {
      const next = new Set([...previous].filter((id) => present.has(id)));
      return next.size === previous.size ? previous : next;
    });
  }, [ids]);

  const shown = byPriority(notices.filter((n) => !hidden.has(n.id)));
  const [top, ...rest] = shown;

  React.useEffect(() => {
    if (rest.length > 0) return;
    // The popover and its trigger leave with the last notice in it; the
    // focus stays in the line instead of falling to the page.
    const line = lineRef.current;
    if (open && line && document.activeElement === document.body) {
      const target = line.querySelector<HTMLElement>('button') ?? line;
      if (target === line) line.tabIndex = -1;
      target.focus();
    }
    setOpen(false);
  }, [open, rest.length]);

  const close = React.useCallback(() => setOpen(false), []);
  const dismiss = (notice: NoticeLineItem) => {
    if (notice.onDismiss) {
      notice.onDismiss();
    } else {
      notice.dismiss?.();
      setHidden((previous) => new Set(previous).add(notice.id));
    }
  };

  if (!top) {
    return null;
  }

  return (
    <div
      ref={lineRef}
      className={cn('flex flex-shrink-0 items-stretch gap-1', className)}
      data-testid="notice-line"
    >
      <NoticeView
        notice={top}
        onDismiss={dismiss}
        className={cn('min-w-0 flex-1', TONE[top.tone])}
      />
      {rest.length > 0 && (
        <>
          <Button
            ref={anchorRef}
            type="button"
            variant="outline"
            size="xs"
            className="h-auto self-stretch"
            aria-haspopup="dialog"
            aria-expanded={open}
            aria-controls={panelId}
            aria-label={ts(
              rest.length === 1
                ? '{count} more notice'
                : '{count} more notices',
              { count: rest.length }
            )}
            onClick={() => setOpen((isOpen) => !isOpen)}
          >
            +{rest.length}
          </Button>
          <AnchoredPopover
            id={panelId}
            open={open}
            onClose={close}
            anchorRef={anchorRef}
            label={ts('More notices')}
          >
            <ul className="space-y-1">
              {rest.map((notice) => (
                <li key={notice.id}>
                  <NoticeView
                    notice={notice}
                    onDismiss={dismiss}
                    className={TONE[notice.tone]}
                  />
                </li>
              ))}
            </ul>
          </AnchoredPopover>
        </>
      )}
    </div>
  );
}

function NoticeView({
  notice,
  onDismiss,
  className,
}: {
  notice: NoticeLineItem;
  onDismiss(notice: NoticeLineItem): void;
  className?: string;
}) {
  const { ts } = useI18n();
  return (
    <div
      role={notice.role ?? 'status'}
      aria-label={notice.label}
      data-notice-id={notice.id}
      className={cn(
        'flex flex-wrap items-center gap-2 rounded-md border px-2 py-1 text-xs',
        className
      )}
    >
      <span className="min-w-0 flex-1 whitespace-normal break-words">
        {notice.message}
      </span>
      {notice.action && (
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="h-6"
          onClick={notice.action.run}
        >
          {notice.action.label}
        </Button>
      )}
      {notice.secondaryAction && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6"
          onClick={notice.secondaryAction.run}
        >
          {notice.secondaryAction.label}
        </Button>
      )}
      {notice.dismissible && (
        <IconButton
          size="sm"
          label={ts('Dismiss')}
          icon={X}
          onClick={() => onDismiss(notice)}
        />
      )}
    </div>
  );
}
