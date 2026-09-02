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
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

type ValueReferenceNotice = components['schemas']['ValueReferenceNotice'];

const REASON_LABELS: Record<string, string> = {
  unknown_step_id: 'Step id does not exist',
  unknown_output_name: 'Output name is not declared',
  missing_dependency: 'Producing step is not a dependency',
  self_reference: 'Step references its own output',
  unknown_context_field: 'Context field is not defined',
  unknown_const_name: 'Const is not declared',
  namespace_unavailable: 'Value is unavailable in this context',
  unknown_env_binding: 'Environment variable is supplied by a run',
};

// Mirrors ValueReferenceNoticeReason.Class() on the server. Only consulted when
// a response predates the class field.
export const DEFECT_REASONS = new Set([
  'unknown_step_id',
  'unknown_output_name',
  'missing_dependency',
  'self_reference',
  'unknown_context_field',
  'unknown_const_name',
]);

export { REASON_LABELS };

// Older servers answer without a class, so fall back to the reason and token.
export function isDefect(notice: ValueReferenceNotice): boolean {
  if (notice.class) {
    return notice.class === 'defect';
  }
  if (
    notice.reason === 'namespace_unavailable' &&
    notice.token?.startsWith('${steps.')
  ) {
    return true;
  }
  return notice.reason ? DEFECT_REASONS.has(notice.reason) : false;
}

function reasonLabel(reason: string): string {
  return REASON_LABELS[reason] ?? reason;
}

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
  const { ts } = useI18n();
  const [open, setOpen] = React.useState(false);

  const defects = React.useMemo(() => notices.filter(isDefect), [notices]);
  const runtimeOnly = React.useMemo(
    () => notices.filter((notice) => !isDefect(notice)),
    [notices]
  );

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
        title={ts('View {label}', { label: ts(label).toLowerCase() })}
        onClick={() => setOpen(true)}
        className={className}
      >
        <Info className="h-3.5 w-3.5" />
        <I18nText text={label} />
        <span className="ml-0.5 rounded-sm bg-muted px-1.5 py-0.5 text-[10px] leading-none text-muted-foreground">
          {defects.length > 0 ? defects.length : notices.length}
        </span>
      </Button>
      <ValueReferenceNoticesDialog
        open={open}
        onOpenChange={setOpen}
        defects={defects}
        runtimeOnly={runtimeOnly}
        description={description}
      />
    </>
  );
}

function ValueReferenceNoticesDialog({
  open,
  onOpenChange,
  defects,
  runtimeOnly,
  description,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  defects: ValueReferenceNotice[];
  runtimeOnly: ValueReferenceNotice[];
  description: string;
}) {
  const [showRuntimeOnly, setShowRuntimeOnly] = React.useState(
    defects.length === 0
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Info className="h-4 w-4 text-muted-foreground" />
            <I18nText text={'Value Reference Notices'} />
          </DialogTitle>
          <DialogDescription className="sr-only">
            {description}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[60vh] space-y-4 overflow-y-auto">
          {defects.length > 0 && (
            <section className="space-y-3">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-foreground">
                <I18nText text={'Needs a fix'} />
              </h3>
              {defects.map((notice, index) => (
                <NoticeCard
                  key={`defect:${notice.fieldPath ?? ''}:${notice.token ?? ''}:${index}`}
                  notice={notice}
                />
              ))}
            </section>
          )}
          {runtimeOnly.length > 0 && (
            <section className="space-y-3">
              <button
                type="button"
                onClick={() => setShowRuntimeOnly((shown) => !shown)}
                className="flex w-full items-center justify-between text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground"
              >
                <span>
                  <I18nText
                    text="Resolved during a run ({count})"
                    values={{ count: runtimeOnly.length }}
                  />
                </span>
                <span aria-hidden="true">{showRuntimeOnly ? '−' : '+'}</span>
              </button>
              {showRuntimeOnly &&
                runtimeOnly.map((notice, index) => (
                  <NoticeCard
                    key={`runtime:${notice.fieldPath ?? ''}:${notice.token ?? ''}:${index}`}
                    notice={notice}
                  />
                ))}
            </section>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function NoticeCard({ notice }: { notice: ValueReferenceNotice }) {
  return (
    <div className="rounded-md border border-border bg-muted/30 p-3 text-sm">
      <p className="whitespace-normal break-words text-foreground">
        {notice.message}
      </p>
      <dl className="mt-2 grid gap-1 text-xs text-muted-foreground sm:grid-cols-[5rem_1fr]">
        {notice.fieldPath && (
          <>
            <dt>
              <I18nText text={'Field'} />
            </dt>
            <dd className="min-w-0 break-all font-mono">{notice.fieldPath}</dd>
          </>
        )}
        {notice.token && (
          <>
            <dt>
              <I18nText text={'Reference'} />
            </dt>
            <dd className="min-w-0 break-all font-mono">{notice.token}</dd>
          </>
        )}
        {notice.reason && (
          <>
            <dt>
              <I18nText text={'Reason'} />
            </dt>
            <dd className="min-w-0 break-words">
              <I18nText text={reasonLabel(notice.reason)} />
            </dd>
          </>
        )}
      </dl>
    </div>
  );
}
