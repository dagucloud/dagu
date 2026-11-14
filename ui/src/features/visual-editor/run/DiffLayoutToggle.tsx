// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { I18nText } from '@/i18n/I18nText';

export interface DiffLayoutToggleProps {
  split: boolean;
  onSplitChange(split: boolean): void;
}

/** The Split and Unified buttons of a diff view. */
export function DiffLayoutToggle({
  split,
  onSplitChange,
}: DiffLayoutToggleProps): ReactElement {
  return (
    <div className="flex gap-1">
      <Button
        variant={split ? 'secondary' : 'ghost'}
        size="xs"
        aria-pressed={split}
        onClick={() => onSplitChange(true)}
      >
        <I18nText text={'Split'} />
      </Button>
      <Button
        variant={split ? 'ghost' : 'secondary'}
        size="xs"
        aria-pressed={!split}
        onClick={() => onSplitChange(false)}
      >
        <I18nText text={'Unified'} />
      </Button>
    </div>
  );
}
