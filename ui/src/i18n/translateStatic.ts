// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locale } from '@/contexts/UserPreference';
import type { StaticMessage } from './locales/en';

export type StaticTranslationValues = Record<string, string | number>;

type TranslatedLocale = Exclude<Locale, 'en'>;
type Catalog = Readonly<Record<StaticMessage, string>>;

// Each catalog is its own chunk, out of the initial bundle, so a reader
// downloads only the locale in use. English needs none: each key is its own
// English text.
const catalogLoaders: Record<TranslatedLocale, () => Promise<Catalog>> = {
  'zh-CN': () => import('./locales/zh-CN').then((module) => module.chinese),
  ja: () => import('./locales/ja').then((module) => module.japanese),
  de: () => import('./locales/de').then((module) => module.german),
  fr: () => import('./locales/fr').then((module) => module.french),
};

const catalogs: Partial<Record<TranslatedLocale, Catalog>> = {};
const loads: Partial<Record<TranslatedLocale, Promise<void>>> = {};

/** Reports whether translateStatic can translate into the locale. */
export function hasStaticMessages(locale: Locale): boolean {
  return locale === 'en' || catalogs[locale] !== undefined;
}

/**
 * Resolves once translateStatic can translate into the locale, or once the
 * load has failed; the source text then stands until the page reloads. Calls
 * for one locale share one load.
 */
export function loadStaticMessages(locale: Locale): Promise<void> {
  if (locale === 'en' || catalogs[locale] !== undefined) {
    return Promise.resolve();
  }
  let load = loads[locale];
  if (!load) {
    load = catalogLoaders[locale]().then(
      (catalog) => {
        catalogs[locale] = catalog;
      },
      (error: unknown) => {
        console.error(`Failed to load the ${locale} message catalog:`, error);
      }
    );
    loads[locale] = load;
  }
  return load;
}

/**
 * Translates English source text into the locale and fills its {name}
 * placeholders from values. The source text stands when the catalog has no
 * entry for it or has not loaded.
 */
export function translateStatic(
  locale: Locale,
  source: string,
  values?: StaticTranslationValues
): string {
  const catalog = locale === 'en' ? undefined : catalogs[locale];
  const message =
    catalog && Object.prototype.hasOwnProperty.call(catalog, source)
      ? catalog[source as StaticMessage]
      : source;
  if (!values) {
    return message;
  }

  return message.replace(/\{(\w+)\}/g, (placeholder, name: string) => {
    const value = values[name];
    return value === undefined ? placeholder : String(value);
  });
}
