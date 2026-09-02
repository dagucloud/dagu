// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { useI18n } from './I18nProvider';
import { translateStatic } from './staticMessages';

const localizedProps = [
  'aria-label',
  'aria-description',
  'placeholder',
  'title',
  'alt',
  'label',
  'description',
  'buttonText',
  'emptyMessage',
  'unavailableMessage',
] as const;

export function I18nProps({
  children,
}: {
  children: React.ReactElement<Record<string, unknown>>;
}): React.ReactElement {
  const { locale } = useI18n();
  const props = { ...children.props };

  for (const name of localizedProps) {
    const value = props[name];
    if (typeof value === 'string') {
      props[name] = translateStatic(locale, value);
    }
  }

  return React.cloneElement(children, props);
}
