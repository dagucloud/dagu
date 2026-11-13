// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { CopyButton } from '@/components/ui/copy-button';
import { useI18n } from '@/i18n/I18nProvider';
import { stepSnippet } from './skill';

/**
 * One line that shows how a chat step or an agent step offers the skill. A
 * skill with an error has no snippet: the server refuses it, so a step that
 * names it fails.
 */
export function SkillUsage({
  name,
  unusable = false,
}: {
  name: string;
  /** The skill has an error. */
  unusable?: boolean;
}): ReactElement {
  const { ts } = useI18n();
  if (unusable) {
    return (
      <p className="text-xs text-destructive">
        {ts('A step cannot use this skill until you fix its errors.')}
      </p>
    );
  }
  return (
    <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
      <span className="shrink-0">{ts('Use it in a chat step')}</span>
      <code
        aria-label={ts('Use it in a chat step')}
        className="min-w-0 truncate rounded border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-foreground"
      >
        {`skills: [${name}]`}
      </code>
      <CopyButton
        size="sm"
        text={stepSnippet(name)}
        label={ts('Copy the step')}
      />
    </div>
  );
}
