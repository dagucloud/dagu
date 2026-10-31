// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import type { ReferenceScope } from '../contracts';
import { ReferenceName } from './UpstreamDataPanel';

/**
 * The OUTPUT column of the node details view on a definition: the action of
 * the step and the outputs that its definition declares. It reads no run.
 */
export function DeclaredOutputsPanel({
  scope,
  stepName,
  action,
  onInsert,
}: {
  scope?: ReferenceScope;
  stepName: string;
  /** Executor type, `call`, `router` or `command`. */
  action?: string | null;
  /** Gives false when no field can take the token. Without it, a click copies. */
  onInsert?(token: string): boolean | void;
}): ReactElement {
  const { ts } = useI18n();
  const outputs = scope?.steps.find((s) => s.name === stepName)?.outputs ?? [];
  return (
    <section
      data-testid="declared-outputs"
      aria-label={ts('Declared outputs')}
      className="flex flex-col gap-2 p-2 text-xs"
    >
      <h3 className="text-xs font-semibold">{ts('Declared outputs')}</h3>
      {action && (
        <p className="text-[10px] text-muted-foreground">
          {ts('Action')}: <span className="font-mono">{action}</span>
        </p>
      )}
      {outputs.length === 0 ? (
        <p className="text-muted-foreground">
          {ts('This step declares no outputs')}
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {outputs.map((output) => (
            <li
              key={output.reference}
              data-testid="declared-output"
              className="flex min-w-0 flex-col gap-0.5 rounded border border-border px-1.5 py-1"
            >
              <div className="flex min-w-0 items-center gap-1">
                <ReferenceName
                  name={output.name}
                  reference={output.reference}
                  onInsert={onInsert}
                />
                {output.type && (
                  <span className="text-[10px] text-muted-foreground">
                    {output.type}
                  </span>
                )}
              </div>
              <code
                title={output.reference}
                className="block min-w-0 truncate font-mono text-[10px] text-muted-foreground"
              >
                {output.reference}
              </code>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
