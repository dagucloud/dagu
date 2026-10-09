// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { Pin } from 'lucide-react';

type Props = {
  /** Workflow name used in the accessible label. */
  name: string;
  pinned: boolean;
  /** Whether the user may pin; otherwise a pinned workflow shows a marker. */
  canToggle: boolean;
  pending?: boolean;
  /** Hide the unpinned button until its row (`group/row`) is hovered or focused. */
  revealOnHover?: boolean;
  onToggle: () => void;
  className?: string;
};

/** Pin button for a workflow, filled while the workflow is pinned. */
function PinToggle({
  name,
  pinned,
  canToggle,
  pending = false,
  revealOnHover = false,
  onToggle,
  className,
}: Props) {
  const { ts } = useI18n();

  if (!canToggle) {
    return pinned ? (
      <span
        role="img"
        aria-label={ts('Pinned')}
        title={ts('Pinned')}
        className={cn(
          'inline-flex size-7 items-center justify-center text-primary',
          className
        )}
      >
        <Pin className="h-3.5 w-3.5 fill-current" />
      </span>
    ) : null;
  }

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      aria-label={ts('Pin workflow {name}', { name })}
      aria-pressed={pinned}
      title={ts(pinned ? 'Unpin workflow' : 'Pin workflow')}
      disabled={pending}
      className={cn(
        pinned ? 'text-primary' : 'text-muted-foreground',
        !pinned &&
          revealOnHover &&
          'opacity-0 focus-visible:opacity-100 group-hover/row:opacity-100 group-focus-within/row:opacity-100 pointer-coarse:opacity-100',
        className
      )}
      onClick={(event) => {
        event.stopPropagation();
        onToggle();
      }}
    >
      <Pin className={cn('h-3.5 w-3.5', pinned && 'fill-current')} />
    </Button>
  );
}

export default PinToggle;
