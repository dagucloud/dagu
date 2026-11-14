// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locale } from '@/contexts/UserPreference';
import type { StaticMessage, staticMessages } from './staticMessages';

export type StaticTranslationValues = Record<string, string | number>;

// The catalogs stay out of the initial bundle. English needs none: each key
// is its own English text.
let catalogs: typeof staticMessages | undefined;
let loading: Promise<void> | undefined;

/** Reports whether translateStatic can translate into the locale. */
export function hasStaticMessages(locale: Locale): boolean {
  return locale === 'en' || catalogs !== undefined;
}

/**
 * Resolves once translateStatic can translate into the locale, or once the
 * load has failed; the source text then stands until the page reloads. Calls
 * share one load.
 */
export function loadStaticMessages(locale: Locale): Promise<void> {
  if (hasStaticMessages(locale)) {
    return Promise.resolve();
  }
  loading ??= import('./staticMessages').then(
    (module) => {
      catalogs = module.staticMessages;
    },
    (error: unknown) => {
      console.error('Failed to load the message catalogs:', error);
    }
  );
  return loading;
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
  const catalog = catalogs?.[locale];
  const message =
    locale !== 'en' &&
    catalog &&
    Object.prototype.hasOwnProperty.call(catalog, source)
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
