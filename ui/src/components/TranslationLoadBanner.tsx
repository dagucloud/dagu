// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * App-wide notice shown when the text of the chosen language failed to load.
 * A tab left open across a server upgrade asks for a chunk that no longer
 * exists, so the notice offers a reload rather than a retry.
 *
 * @module components
 */
import { useI18n } from '@/i18n/I18nProvider';

export function TranslationLoadBanner() {
  // The shell catalog is in the main bundle, so its text is always there.
  const { catalogFailed, t } = useI18n();
  if (!catalogFailed) {
    return null;
  }

  return (
    <div
      role="alert"
      className="border-b border-warning/30 bg-warning/10 px-4 py-1.5 flex items-center justify-between gap-2 text-sm text-warning"
    >
      <span>{t('language.loadFailed')}</span>
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="underline hover:no-underline"
      >
        {t('common.reload')}
      </button>
    </div>
  );
}
