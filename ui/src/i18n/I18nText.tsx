// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useI18n } from './I18nProvider';
import {
  translateStatic,
  type StaticTranslationValues,
} from './staticMessages';

export function I18nText({
  text,
  values,
}: {
  text: string;
  values?: StaticTranslationValues;
}): string {
  const { locale } = useI18n();
  return translateStatic(locale, text, values);
}
