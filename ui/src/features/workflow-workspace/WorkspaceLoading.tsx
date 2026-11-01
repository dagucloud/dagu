// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import LoadingIndicator from '@/components/ui/loading-indicator';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * What the workflow page shows until its data is there: the bar with the name
 * of the workflow, in the place and the size of the real bar, and the loader
 * in the middle of the area. The name comes from the address, so the first
 * row is already where it stays and nothing moves when the page loads. It is
 * the fallback of the route too, so the two loading states look the same.
 */
export function WorkspaceLoading({ fileName }: { fileName: string }) {
  const { ts } = useI18n();
  return (
    <div
      role="status"
      aria-label={ts('Loading...')}
      data-testid="ww-loading"
      className="flex h-full min-h-0 w-full flex-col"
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-border px-3">
        {/* The place of the pin button of the bar. */}
        <span aria-hidden="true" className="hidden size-7 shrink-0 md:block" />
        <h1 className="min-w-0 truncate text-base font-semibold tracking-[-0.01em]">
          {fileName}
        </h1>
      </div>
      <div className="flex min-h-0 flex-1 items-center justify-center">
        <LoadingIndicator />
      </div>
    </div>
  );
}
