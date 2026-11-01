// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Slot } from '@radix-ui/react-slot';
import * as React from 'react';

import { cn } from '@/lib/utils';

/**
 * Centered placeholder for screens that have nothing to show: an empty
 * collection, a missing resource, or a failed load.
 */
function Empty({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty"
      className={cn(
        'flex min-w-0 flex-col items-center gap-5 px-6 py-12 text-center text-balance md:py-16',
        className
      )}
      {...props}
    />
  );
}

function EmptyHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty-header"
      className={cn('flex max-w-md flex-col items-center gap-2', className)}
      {...props}
    />
  );
}

function EmptyMedia({
  className,
  tone = 'muted',
  ...props
}: React.ComponentProps<'div'> & { tone?: 'muted' | 'error' }) {
  return (
    <div
      data-slot="empty-media"
      aria-hidden="true"
      className={cn(
        'mb-1 flex size-10 shrink-0 items-center justify-center rounded-lg border [&_svg]:size-5',
        tone === 'error'
          ? 'border-error/30 bg-error-muted text-error'
          : 'border-border bg-muted text-muted-foreground',
        className
      )}
      {...props}
    />
  );
}

function EmptyTitle({
  className,
  asChild = false,
  ...props
}: React.ComponentProps<'h2'> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : 'h2';
  return (
    <Comp
      data-slot="empty-title"
      className={cn(
        'text-base font-semibold tracking-tight text-foreground',
        className
      )}
      {...props}
    />
  );
}

function EmptyDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return (
    <p
      data-slot="empty-description"
      className={cn(
        'text-sm/relaxed break-words text-muted-foreground [&_code]:rounded-sm [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-foreground',
        className
      )}
      {...props}
    />
  );
}

function EmptyContent({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty-content"
      className={cn(
        'flex flex-wrap items-center justify-center gap-2',
        className
      )}
      {...props}
    />
  );
}

export {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
};
