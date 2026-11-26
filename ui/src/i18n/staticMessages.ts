// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The static catalogs of every locale, for checks that compare locales. The
 * app loads one locale at a time through translateStatic instead.
 *
 * @module i18n/staticMessages
 */
import type { Locale } from '@/contexts/UserPreference';
import { baseGerman, german } from './locales/de';
import {
  type BaseStaticMessage,
  baseEnglish,
  english,
  type StaticMessage,
} from './locales/en';
import { baseFrench, french } from './locales/fr';
import { baseJapanese, japanese } from './locales/ja';
import { baseChinese, chinese } from './locales/zh-CN';

export const baseStaticMessages = {
  en: baseEnglish,
  'zh-CN': baseChinese,
  ja: baseJapanese,
  de: baseGerman,
  fr: baseFrench,
} satisfies Record<Locale, Record<BaseStaticMessage, string>>;

export const staticMessages = {
  en: english,
  'zh-CN': chinese,
  ja: japanese,
  de: german,
  fr: french,
} satisfies Record<Locale, Record<StaticMessage, string>>;
