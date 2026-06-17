// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, Info } from 'lucide-react';
import React from 'react';
import { components } from '../../../../api/v1/schema';
import { cn } from '../../../../lib/utils';

type Diagnostic = components['schemas']['Diagnostic'];

type DAGRunDiagnosticsProps = {
  diagnostics?: Diagnostic[];
};

const levelStyles: Record<string, string> = {
  error: 'text-destructive',
  warning: 'text-amber-600 dark:text-amber-400',
  notice: 'text-muted-foreground',
};

const DAGRunDiagnostics: React.FC<DAGRunDiagnosticsProps> = ({
  diagnostics,
}) => {
  const [open, setOpen] = React.useState(false);

  if (!diagnostics || diagnostics.length === 0) {
    return null;
  }

  return (
    <div className="mt-4 border-t border-border pt-3">
      <button
        type="button"
        className="inline-flex h-8 items-center gap-2 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        title="Show run diagnostics"
      >
        <Info className="h-4 w-4" />
        <span>Diagnostics</span>
        <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[11px] leading-none text-muted-foreground">
          {diagnostics.length}
        </span>
        <ChevronDown
          className={cn(
            'h-3.5 w-3.5 transition-transform',
            open && 'rotate-180'
          )}
        />
      </button>

      {open && (
        <div className="mt-2 divide-y divide-border border-t border-border">
          {diagnostics.map((diagnostic, index) => {
            const levelClass =
              levelStyles[diagnostic.level] || 'text-muted-foreground';

            return (
              <div
                key={`${diagnostic.code}-${diagnostic.field || ''}-${diagnostic.token || ''}-${index}`}
                className="grid gap-1 py-2 text-xs md:grid-cols-[9rem_1fr]"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span
                    className={cn(
                      'font-medium capitalize leading-5',
                      levelClass
                    )}
                  >
                    {diagnostic.level}
                  </span>
                </div>
                <div className="min-w-0 space-y-1">
                  <div className="break-words text-foreground">
                    {diagnostic.message}
                  </div>
                  <div className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono text-[11px] text-muted-foreground">
                    {diagnostic.field && (
                      <span className="break-all">{diagnostic.field}</span>
                    )}
                    {diagnostic.token && (
                      <span className="break-all">{diagnostic.token}</span>
                    )}
                    <span className="break-all">{diagnostic.code}</span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default DAGRunDiagnostics;
