// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check, Copy, type LucideIcon } from 'lucide-react';

import { IconButton, type IconButtonProps } from '@/components/ui/icon-button';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { useI18n } from '@/i18n/I18nProvider';

export interface CopyButtonProps
  extends Omit<
    IconButtonProps,
    'icon' | 'label' | 'onClick' | 'pressed' | 'iconClassName'
  > {
  /** The text, or a function that returns it when the user clicks. */
  text: string | (() => string | Promise<string>);
  label: string;
  /** Name shown for 2000 ms after the copy. Defaults to "Copied". */
  copiedLabel?: string;
  /** Defaults to Copy. */
  icon?: LucideIcon;
  /** Called once each time the text reached the clipboard. */
  onCopied?: () => void;
}

/**
 * An icon-only copy action. On success the icon becomes a Check in the
 * success colour, and the name becomes the copied label, for 2000 ms.
 */
export function CopyButton({
  text,
  label,
  copiedLabel,
  icon = Copy,
  onCopied,
  ...props
}: CopyButtonProps) {
  const { ts } = useI18n();
  const { copied, copy } = useCopyFeedback(2000, onCopied);

  const onClick = async () => {
    let value: string;
    try {
      value = typeof text === 'function' ? await text() : text;
    } catch {
      // A text that cannot be read is a failed copy: the button stays idle.
      return;
    }
    await copy(value);
  };

  return (
    <IconButton
      {...props}
      label={copied ? (copiedLabel ?? ts('Copied')) : label}
      icon={copied ? Check : icon}
      iconClassName={copied ? 'text-success' : undefined}
      onClick={() => void onClick()}
    />
  );
}
