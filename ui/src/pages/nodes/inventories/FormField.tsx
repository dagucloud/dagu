// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Label } from '@/components/ui/label';
import { I18nText } from '@/i18n/I18nText';
import React from 'react';

/** A labeled input of an inventory or node form. */
export function Field({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: React.ReactElement;
}): React.ReactElement {
  const id = `inventory-${label.toLowerCase().replace(/\s+/g, '-')}`;
  return (
    <div className={className}>
      <Label htmlFor={id} className="mb-1 block text-xs">
        <I18nText text={label} />
      </Label>
      {React.cloneElement(children as React.ReactElement<{ id?: string }>, {
        id,
      })}
    </div>
  );
}
