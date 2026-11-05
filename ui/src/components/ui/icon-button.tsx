// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LucideIcon } from 'lucide-react';
import * as React from 'react';

import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { Spinner } from '@/components/ui/spinner';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  useInsideTooltip,
} from '@/components/ui/tooltip';
import { cn, FOCUS_RING } from '@/lib/utils';

export interface IconButtonProps
  extends Omit<React.ComponentProps<'button'>, 'children' | 'aria-label'> {
  /** The name of the action: its accessible name and the tooltip title. */
  label: string;
  icon: LucideIcon;
  /** sm is 24 px (node toolbars and rows); md is 28 px (elsewhere). */
  size?: 'sm' | 'md';
  tone?: 'default' | 'destructive';
  /** The button is a toggle: set aria-pressed and a pressed background. */
  pressed?: boolean;
  /** Show a spinner in the icon slot and ignore clicks. The box is unchanged. */
  busy?: boolean;
  /** Disables the button and shows this text in the tooltip. */
  disabledReason?: string;
  /** The key text that the action has, for example from canvasShortcut(). */
  shortcut?: string;
  /** A second tooltip line. */
  description?: string;
  /** Extra classes for the icon, for example a status colour. */
  iconClassName?: string;
  tooltipSide?: React.ComponentProps<typeof TooltipContent>['side'];
}

// A touch screen gets larger targets; a mouse keeps the dense sizes.
const SIZE_CLASS = {
  sm: 'size-6 pointer-coarse:size-8 [&_svg]:size-3.5',
  md: 'size-7 pointer-coarse:size-9 [&_svg]:size-4',
} as const;

const LabelsContext = React.createContext(false);

/**
 * Shows the names of the icon buttons inside it next to their icons, where
 * a tooltip does not work, such as a sheet on a touch screen.
 */
export function IconButtonLabels({ children }: { children: React.ReactNode }) {
  return (
    <LabelsContext.Provider value={true}>{children}</LabelsContext.Provider>
  );
}

/** True inside IconButtonLabels: an icon action there shows its name. */
export function useIconButtonLabels(): boolean {
  return React.useContext(LabelsContext);
}

/** Reads IconButtonLabels where an element renders, for a custom icon action. */
export const IconButtonLabelsConsumer = LabelsContext.Consumer;

/**
 * An icon-only action. The name is in the tooltip and in aria-label, also
 * while the button is disabled or busy. Inside IconButtonLabels the name
 * also shows next to the icon.
 */
export function IconButton({
  label,
  icon: Icon,
  size = 'md',
  tone = 'default',
  pressed,
  busy = false,
  disabledReason,
  shortcut,
  description,
  iconClassName,
  tooltipSide = 'bottom',
  disabled,
  className,
  onClick,
  type = 'button',
  ...buttonProps
}: IconButtonProps) {
  const insideTooltip = useInsideTooltip();
  const labeled = useIconButtonLabels();
  const isDisabled = Boolean(disabled || disabledReason);
  const button = (
    <Button
      {...buttonProps}
      type={type}
      variant="ghost"
      title={insideTooltip ? label : buttonProps.title}
      aria-label={label}
      aria-pressed={pressed}
      aria-busy={busy || undefined}
      disabled={isDisabled}
      onClick={busy ? undefined : onClick}
      className={cn(
        'p-0',
        labeled
          ? 'h-auto min-h-9 w-full justify-start gap-2 whitespace-normal px-2.5 py-1.5 text-left text-sm font-normal [&_svg]:size-4 [&_svg]:shrink-0'
          : SIZE_CLASS[size],
        FOCUS_RING,
        tone === 'destructive' && 'hover:text-destructive',
        pressed && 'bg-muted text-foreground',
        className
      )}
    >
      {busy ? (
        <Spinner />
      ) : (
        <Icon className={iconClassName} aria-hidden="true" />
      )}
      {labeled && <span aria-hidden="true">{label}</span>}
    </Button>
  );

  // A tooltip in a tooltip would close the outer one: the title says the name.
  if (insideTooltip) return button;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {/* A disabled button takes no pointer or focus event: its wrapper does. */}
        {isDisabled ? (
          <span
            className={labeled ? 'flex w-full' : 'inline-flex shrink-0'}
            tabIndex={0}
          >
            {button}
          </span>
        ) : (
          button
        )}
      </TooltipTrigger>
      <TooltipContent side={tooltipSide}>
        <div className="flex flex-col gap-0.5">
          <span className="flex items-center gap-2 font-medium">
            {label}
            {shortcut && <Kbd>{shortcut}</Kbd>}
          </span>
          {description && (
            <span className="text-muted-foreground">{description}</span>
          )}
          {disabledReason && (
            <span className="text-warning">{disabledReason}</span>
          )}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}
