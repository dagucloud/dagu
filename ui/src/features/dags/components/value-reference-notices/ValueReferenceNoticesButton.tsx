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

type ValueReferenceNotice = components['schemas']['ValueReferenceNotice'];

type ValueReferenceNoticesButtonProps = {
  notices: ValueReferenceNotice[];
  description: string;
  label?: string;
  size?: React.ComponentProps<typeof Button>['size'];
  variant?: React.ComponentProps<typeof Button>['variant'];
  className?: string;
};

export function ValueReferenceNoticesButton({
  notices,
  description,
  label = 'Notices',
  size = 'xs',
  variant = 'ghost',
  className,
}: ValueReferenceNoticesButtonProps) {
  const [open, setOpen] = React.useState(false);

  React.useEffect(() => {
    if (notices.length === 0) {
      setOpen(false);
    }
  }, [notices.length]);

  if (notices.length === 0) {
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
          {notices.length}
        </span>
      </Button>
      <ValueReferenceNoticesDialog
        open={open}
        onOpenChange={setOpen}
        notices={notices}
        description={description}
      />
    </>
  );
}

function ValueReferenceNoticesDialog({
  open,
  onOpenChange,
  notices,
  description,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  notices: ValueReferenceNotice[];
  description: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Info className="h-4 w-4 text-muted-foreground" />
            Value Reference Notices
          </DialogTitle>
          <DialogDescription className="sr-only">
            {description}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[60vh] space-y-3 overflow-y-auto">
          {notices.map((notice, index) => (
            <div
              key={`${notice.fieldPath ?? ''}:${notice.token ?? ''}:${index}`}
              className="rounded-md border border-border bg-muted/30 p-3 text-sm"
            >
              <p className="text-foreground">{notice.message}</p>
              <dl className="mt-2 grid gap-1 text-xs text-muted-foreground sm:grid-cols-[5rem_1fr]">
                {notice.fieldPath && (
                  <>
                    <dt>Field</dt>
                    <dd className="min-w-0 break-all font-mono">
                      {notice.fieldPath}
                    </dd>
                  </>
                )}
                {notice.token && (
                  <>
                    <dt>Reference</dt>
                    <dd className="min-w-0 break-all font-mono">
                      {notice.token}
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
