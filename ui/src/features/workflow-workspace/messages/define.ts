// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Declares the static messages of one task module. The English value of each
 * key is the key itself. The zh-CN and ja catalogs must have the same keys.
 */
export function defineMessages<const E extends Record<string, string>>(m: {
  en: E;
  'zh-CN': { [K in keyof E]: string };
  ja: { [K in keyof E]: string };
}): typeof m {
  return m;
}
