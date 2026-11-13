// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { Pin } from 'lucide-react';

type Props = {
  /** Name of the workflow or group, used in the accessible label. */
  name: string;
  /** What the button pins: a workflow (default) or a whole group. */
  subject?: 'workflow' | 'group';
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
  subject = 'workflow',
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
      aria-label={ts(
        subject === 'group' ? 'Pin group {name}' : 'Pin workflow {name}',
        { name }
      )}
      aria-pressed={pinned}
      title={ts(
        subject === 'group'
          ? pinned
            ? 'Unpin group'
            : 'Pin group'
          : pinned
            ? 'Unpin workflow'
            : 'Pin workflow'
      )}
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
