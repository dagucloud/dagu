// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { History, X } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';

/**
 * Says that a run list shows the runs of one backfill, links to it, and
 * clears the scope.
 */
export function BackfillRunsScope({
  backfillId,
  onClear,
}: {
  backfillId: string;
  onClear: () => void;
}) {
  const { ts } = useI18n();
  return (
    <div className="mb-3 flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-1.5 text-sm">
      <History
        className="h-4 w-4 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1">
        <I18nTemplate
          text="Runs of backfill {id}"
          values={{
            id: (
              <Link
                to={`/backfills/${encodeURIComponent(backfillId)}`}
                className="font-mono underline-offset-2 hover:underline focus-visible:underline focus-visible:outline-none"
              >
                {backfillId}
              </Link>
            ),
          }}
        />
      </span>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={ts('Show all runs')}
        onClick={onClear}
      >
        <X />
      </Button>
    </div>
  );
}
