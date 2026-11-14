// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { createPortal } from 'react-dom';
import { useI18n } from '@/i18n/I18nProvider';

export interface RenameStepPopoverProps {
  /** The node element; the popover covers its top edge. */
  anchor: HTMLElement | null;
  name: string;
  /** Called once, with a new name that is not empty. */
  onSubmit(newName: string): void;
  onCancel(): void;
}

/**
 * A name field over a node. Enter keeps the new name, Esc and a click
 * elsewhere cancel.
 */
export function RenameStepPopover({
  anchor,
  name,
  onSubmit,
  onCancel,
}: RenameStepPopoverProps): ReactElement | null {
  const { ts } = useI18n();
  const [value, setValue] = useState(name);
  const [place, setPlace] = useState<{
    left: number;
    top: number;
    width: number;
  } | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const done = useRef(false);

  useLayoutEffect(() => {
    if (!anchor) {
      setPlace(null);
      return;
    }
    const rect = anchor.getBoundingClientRect();
    setPlace({
      left: rect.left,
      top: rect.top,
      width: Math.max(rect.width, 192),
    });
  }, [anchor]);

  const placed = place !== null;
  useEffect(() => {
    if (placed) {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [placed]);

  if (!anchor || !place) {
    return null;
  }

  const finish = (submit: boolean) => {
    if (done.current) {
      return;
    }
    done.current = true;
    const next = value.trim();
    if (submit && next !== '' && next !== name) {
      onSubmit(next);
    } else {
      onCancel();
    }
  };

  return createPortal(
    <div
      data-testid="ve-canvas-rename"
      className="fixed z-[70] rounded-md border bg-popover p-1 shadow-md"
      style={{ left: place.left, top: place.top, width: place.width }}
    >
      <input
        ref={inputRef}
        aria-label={ts('Step name')}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          // The field is in a portal: keys must not reach the editor around it.
          event.stopPropagation();
          if (event.nativeEvent.isComposing) {
            return;
          }
          if (event.key === 'Enter') {
            event.preventDefault();
            finish(true);
          } else if (event.key === 'Escape') {
            event.preventDefault();
            finish(false);
          }
        }}
        onBlur={() => finish(false)}
        className="h-7 w-full rounded-sm border border-input bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
      />
    </div>,
    document.body
  );
}
