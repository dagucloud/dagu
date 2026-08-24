// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import { Check, Copy } from 'lucide-react';
import { useEffect, useState } from 'react';

type AuditEntry = components['schemas']['AuditEntry'];

type AuditEntryDetailsDrawerProps = {
  entry: AuditEntry | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

type DetailField = {
  label: string;
  value?: string;
};

function formatDetails(details?: string): string {
  if (!details) {
    return '';
  }

  try {
    return JSON.stringify(JSON.parse(details), null, 2);
  } catch {
    return details;
  }
}

function DetailSection({
  fields,
  title,
}: {
  fields: DetailField[];
  title: string;
}) {
  return (
    <section>
      <h3 className="border-b border-border pb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </h3>
      <dl className="mt-2 divide-y divide-border/60">
        {fields.map((field) => (
          <div
            key={field.label}
            className="grid gap-1 py-2 sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-4"
          >
            <dt className="text-xs text-muted-foreground">{field.label}</dt>
            <dd className="break-all font-mono text-xs text-foreground">
              {field.value || '—'}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export function resultVariant(value?: string) {
  if (value === 'succeeded') return 'success';
  if (value === 'failed') return 'error';
  if (value === 'denied') return 'warning';
  return 'outline';
}

export function AuditEntryDetailsDrawer({
  entry,
  open,
  onOpenChange,
}: AuditEntryDetailsDrawerProps) {
  const config = useConfig();
  const [activeTab, setActiveTab] = useState<'details' | 'raw'>('details');
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setActiveTab('details');
    setCopied(false);
  }, [entry?.id]);

  if (!entry) {
    return null;
  }

  const timestamp =
    config.tzOffsetInSec !== undefined
      ? dayjs(entry.timestamp)
          .utcOffset(config.tzOffsetInSec / 60)
          .format('MMM D, YYYY HH:mm:ss')
      : dayjs(entry.timestamp).format('MMM D, YYYY HH:mm:ss');
  const details = formatDetails(entry.details);
  const rawEntry = JSON.stringify(entry, null, 2);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(rawEntry);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="left-auto right-0 top-0 flex h-[100dvh] max-h-[100dvh] w-full max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden p-0 sm:w-[min(48rem,calc(100vw-2rem))] sm:rounded-l-md sm:rounded-r-none">
        <DialogHeader className="shrink-0 border-b border-border px-3 py-2 pr-14">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <DialogTitle>Audit entry</DialogTitle>
                {entry.result ? (
                  <Badge variant={resultVariant(entry.result)}>
                    {entry.result}
                  </Badge>
                ) : null}
              </div>
              <DialogDescription className="break-all font-mono text-xs">
                {entry.action}
              </DialogDescription>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void handleCopy()}
              aria-live="polite"
            >
              {copied ? (
                <Check className="h-4 w-4" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
              {copied ? 'Copied' : 'Copy JSON'}
            </Button>
          </div>
        </DialogHeader>

        <div
          className="flex shrink-0 border-b border-border px-5"
          role="tablist"
          aria-label="Audit entry views"
        >
          {(['details', 'raw'] as const).map((tab) => {
            const label = tab === 'details' ? 'Details' : 'Raw JSON';
            const selected = activeTab === tab;
            return (
              <button
                key={tab}
                id={`audit-entry-${tab}-tab`}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-controls={`audit-entry-${tab}-panel`}
                tabIndex={selected ? 0 : -1}
                onClick={() => setActiveTab(tab)}
                onKeyDown={(event) => {
                  if (
                    event.key !== 'ArrowLeft' &&
                    event.key !== 'ArrowRight' &&
                    event.key !== 'Home' &&
                    event.key !== 'End'
                  ) {
                    return;
                  }

                  event.preventDefault();
                  const nextTab =
                    event.key === 'ArrowRight' || event.key === 'End'
                      ? 'raw'
                      : 'details';
                  setActiveTab(nextTab);
                  window.requestAnimationFrame(() => {
                    document
                      .getElementById(`audit-entry-${nextTab}-tab`)
                      ?.focus();
                  });
                }}
                className={cn(
                  'h-11 border-b-2 px-3 text-xs font-medium transition-colors',
                  selected
                    ? 'border-primary text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                )}
              >
                {label}
              </button>
            );
          })}
        </div>

        {activeTab === 'details' ? (
          <div
            id="audit-entry-details-panel"
            role="tabpanel"
            aria-labelledby="audit-entry-details-tab"
            tabIndex={0}
            className="min-h-0 flex-1 space-y-6 overflow-y-auto px-5 py-4"
          >
            <DetailSection
              title="Event"
              fields={[
                { label: 'Audit ID', value: entry.id },
                { label: 'Timestamp', value: timestamp },
                { label: 'Category', value: entry.category },
                { label: 'Action', value: entry.action },
                { label: 'Result', value: entry.result },
                { label: 'Source', value: entry.source },
                { label: 'Surface', value: entry.surface },
              ]}
            />
            <DetailSection
              title="Identity & Access"
              fields={[
                { label: 'Username', value: entry.username },
                { label: 'User ID', value: entry.userId },
                { label: 'Workspace', value: entry.workspace },
                { label: 'IP address', value: entry.ipAddress },
                { label: 'Credential type', value: entry.credentialType },
                { label: 'Credential ID', value: entry.credentialId },
              ]}
            />
            <DetailSection
              title="Resource & Trace"
              fields={[
                { label: 'Resource type', value: entry.resourceType },
                { label: 'Resource ID', value: entry.resourceId },
                { label: 'MCP tool', value: entry.mcpTool },
                { label: 'Correlation ID', value: entry.correlationId },
              ]}
            />
            <section>
              <h3 className="border-b border-border pb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Details
              </h3>
              {details ? (
                <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/60 p-3 font-mono text-xs leading-5 text-foreground">
                  {details}
                </pre>
              ) : (
                <p className="mt-3 text-sm text-muted-foreground">
                  No additional details.
                </p>
              )}
            </section>
          </div>
        ) : (
          <div
            id="audit-entry-raw-panel"
            role="tabpanel"
            aria-labelledby="audit-entry-raw-tab"
            tabIndex={0}
            className="min-h-0 flex-1 overflow-auto bg-muted/30 p-5"
          >
            <pre className="whitespace-pre-wrap break-words rounded-md border border-border bg-background p-4 font-mono text-xs leading-5 text-foreground">
              {rawEntry}
            </pre>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
