// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { TriangleAlert } from 'lucide-react';
import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { CopyButton } from '@/components/ui/copy-button';
import { shortHash } from '@/features/skills/skillSync';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The banner of a write that the server refused with 409: the skill changed
 * after the editor loaded it. The draft stays in the editor. The user copies
 * it, keeps it on the version of the server, or loads the server version.
 */
export function SkillConflictBanner({
  hash,
  draft,
  busy,
  onKeep,
  onReload,
}: {
  /** The hash on the server now. Empty when the skill does not exist. */
  hash: string;
  /** The unsaved text of the file that the user edits. */
  draft: string;
  busy: boolean;
  onKeep(): void;
  onReload(): void;
}): ReactElement {
  const { ts } = useI18n();
  const gone = hash === '';
  return (
    <div
      role="alert"
      data-testid="skill-conflict"
      className="space-y-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs"
    >
      <div className="flex items-start gap-2">
        <TriangleAlert
          className="mt-0.5 size-3.5 shrink-0 text-warning"
          aria-hidden
        />
        <div className="min-w-0 space-y-0.5">
          <p className="font-medium">
            {gone
              ? ts('The skill no longer exists on the server.')
              : ts('The skill changed on the server.')}
          </p>
          <p className="text-muted-foreground">
            {gone
              ? ts('Your changes are still in the editor. Nothing was saved.')
              : ts(
                  'Your changes are still in the editor. Nothing was saved. Version on the server: {hash}.',
                  { hash: shortHash(hash) }
                )}
          </p>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2 pl-5">
        <CopyButton size="sm" text={draft} label={ts('Copy my changes')} />
        {!gone && (
          <Button
            variant="outline"
            size="sm"
            disabled={busy}
            title={ts(
              'Keep your text in the editor. The next save replaces this file on the server.'
            )}
            onClick={onKeep}
          >
            {ts('Keep my changes')}
          </Button>
        )}
        <Button variant="outline" size="sm" disabled={busy} onClick={onReload}>
          {ts('Discard my changes and reload')}
        </Button>
      </div>
    </div>
  );
}
