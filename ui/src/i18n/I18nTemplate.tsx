// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Fragment, type ReactNode } from 'react';
import { useI18n } from './I18nProvider';

export function I18nTemplate({
  text,
  values,
}: {
  text: string;
  values: Record<string, ReactNode>;
}): ReactNode {
  const { ts } = useI18n();

  return ts(text)
    .split(/(\{\w+\})/g)
    .map((part, index) => {
      const name = part.match(/^\{(\w+)\}$/)?.[1];
      const value =
        name && Object.prototype.hasOwnProperty.call(values, name)
          ? values[name]
          : part;

      return <Fragment key={index}>{value}</Fragment>;
    });
}
