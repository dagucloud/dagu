// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { I18nText } from '@/i18n/I18nText';
import React from 'react';
import type { ChangeRefusal, Inventory } from './inventoryChanges';

/**
 * Says why the server refused a change: each broken rule, or, when the
 * inventory changed since it was read, an offer to load it as it is now.
 */
export function RefusalNotice({
  refusal,
  onLoadCurrent,
}: {
  refusal: ChangeRefusal;
  onLoadCurrent: (current: Inventory) => void;
}): React.ReactElement {
  const { current } = refusal;
  return (
    <div role="alert" className="space-y-1 text-xs text-error">
      <p>{refusal.message}</p>
      {refusal.rules.length > 0 && (
        <ul className="list-disc space-y-0.5 pl-4 font-mono">
          {refusal.rules.map((rule) => (
            <li key={rule}>{rule}</li>
          ))}
        </ul>
      )}
      {current && (
        <div className="flex flex-wrap items-center gap-2 text-foreground">
          <I18nText
            text={
              'Someone changed this inventory after you opened it. Load it as it is now, then make your change again.'
            }
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => onLoadCurrent(current)}
          >
            <I18nText text={'Load current version'} />
          </Button>
        </div>
      )}
    </div>
  );
}
