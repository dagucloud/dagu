// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, Split, Trash2, Undo2 } from 'lucide-react';
import type { ComponentPropsWithoutRef, ReactElement } from 'react';
import { cn } from '@/lib/utils';

const BASE_CLASS =
  'nodrag nopan pointer-events-auto flex h-6 w-6 items-center justify-center rounded-full border-[1.5px] border-muted-foreground bg-card text-muted-foreground shadow-sm transition-opacity focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';

const KIND_CLASS = {
  add: 'hover:border-primary hover:bg-primary hover:text-primary-foreground',
  parallel:
    'hover:border-primary hover:bg-primary hover:text-primary-foreground',
  restore:
    'hover:border-primary hover:bg-primary hover:text-primary-foreground',
  remove:
    'hover:border-destructive hover:bg-destructive hover:text-destructive-foreground',
} as const;

const ICONS = {
  add: Plus,
  parallel: Split,
  restore: Undo2,
  remove: Trash2,
} as const;

export interface LinkButtonProps
  extends Omit<ComponentPropsWithoutRef<'button'>, 'type' | 'children'> {
  /**
   * 'add' is the round "+" of a link and of the end of the graph; 'parallel'
   * adds a step beside the target of the link; 'restore' writes the link of an
   * inferred dependency again; 'remove' is the trash of a link.
   */
  kind: keyof typeof KIND_CLASS;
}

/**
 * Round button on a link: the "+" that adds a step at the end of the graph or
 * between two steps, the router icon turned to the right that adds a step in
 * parallel, the turned arrow that restores an inferred link, and the trash
 * that removes a link. All have the same
 * size, the same 1.5 px border in the colour of the edge stroke and the same
 * hover, so that the "+" looks the same on every link.
 */
export function LinkButton({
  kind,
  className,
  ...props
}: LinkButtonProps): ReactElement {
  const Icon = ICONS[kind];
  return (
    <button
      type="button"
      {...props}
      className={cn(BASE_CLASS, KIND_CLASS[kind], className)}
    >
      {/* The icon of router.route has its stem down: a quarter turn makes it point right. */}
      <Icon
        className={cn('h-3 w-3', kind === 'parallel' && 'rotate-90')}
        aria-hidden="true"
      />
    </button>
  );
}
