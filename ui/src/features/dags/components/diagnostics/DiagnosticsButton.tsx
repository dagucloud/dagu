// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Info } from 'lucide-react';
import React from 'react';
import { components } from '../../../../api/v1/schema';

type OperationDiagnostic = components['schemas']['Diagnostic'];

type DiagnosticsButtonProps = {
  diagnostics: OperationDiagnostic[];
  description: string;
  label?: string;
  size?: React.ComponentProps<typeof Button>['size'];
  variant?: React.ComponentProps<typeof Button>['variant'];
  className?: string;
};

export function DiagnosticsButton({
  diagnostics,
  description,
  label = 'Notices',
  size = 'xs',
  variant = 'ghost',
  className,
}: DiagnosticsButtonProps) {
  const [open, setOpen] = React.useState(false);

  React.useEffect(() => {
    if (diagnostics.length === 0) {
      setOpen(false);
    }
  }, [diagnostics.length]);

  if (diagnostics.length === 0) {
    return null;
  }

  return (
    <>
      <Button
        variant={variant}
        size={size}
        title={`View ${label.toLowerCase()}`}
        onClick={() => setOpen(true)}
        className={className}
      >
        <Info className="h-3.5 w-3.5" />
        {label}
        <span className="ml-0.5 rounded-sm bg-muted px-1.5 py-0.5 text-[10px] leading-none text-muted-foreground">
          {diagnostics.length}
        </span>
      </Button>
      <DiagnosticsDialog
        open={open}
        onOpenChange={setOpen}
        diagnostics={diagnostics}
        description={description}
      />
    </>
  );
}

function DiagnosticsDialog({
  open,
  onOpenChange,
  diagnostics,
  description,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  diagnostics: OperationDiagnostic[];
  description: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Info className="h-4 w-4 text-muted-foreground" />
            Diagnostics
          </DialogTitle>
          <DialogDescription className="sr-only">
            {description}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[60vh] space-y-3 overflow-y-auto">
          {diagnostics.map((diagnostic, index) => (
            <div
              key={`${diagnostic.kind}:${diagnostic.code}:${diagnostic.location?.fieldPath ?? ''}:${index}`}
              className="rounded-md border border-border bg-muted/30 p-3 text-sm"
            >
              <p className="text-foreground">{diagnostic.message}</p>
              <dl className="mt-2 grid gap-1 text-xs text-muted-foreground sm:grid-cols-[5rem_1fr]">
                <dt>Code</dt>
                <dd className="min-w-0 break-all font-mono">
                  {diagnostic.kind}.{diagnostic.code}
                </dd>
                {diagnostic.location?.fieldPath && (
                  <>
                    <dt>Field</dt>
                    <dd className="min-w-0 break-all font-mono">
                      {diagnostic.location.fieldPath}
                    </dd>
                  </>
                )}
                {diagnostic.attributes?.token && (
                  <>
                    <dt>Reference</dt>
                    <dd className="min-w-0 break-all font-mono">
                      {diagnostic.attributes.token}
                    </dd>
                  </>
                )}
              </dl>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
