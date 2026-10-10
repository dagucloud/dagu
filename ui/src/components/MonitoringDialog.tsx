// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useId, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { LicenseMonitoringLevel } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

type ReportingLevel =
  | LicenseMonitoringLevel.health
  | LicenseMonitoringLevel.runs;

/** What each level sends, worded as RFC 002 lists it. */
const choices: { level: ReportingLevel; title: string; sends: string }[] = [
  {
    level: LicenseMonitoringLevel.health,
    title: 'Health only',
    sends:
      'Sends the Dagu version, OS, architecture, process start time, and service counts: schedulers holding the scheduler lock, and registered coordinators.',
  },
  {
    level: LicenseMonitoringLevel.runs,
    title: 'Health and run status (recommended)',
    sends:
      'Sends the health fields, plus each reportable status change of a top-level DAG run: event ID, type, DAG name, run ID, attempt ID, status, the event’s time, and the run’s queued, started, and finished times; on a finished run, up to 50 failed step names.',
  },
];

/**
 * Asks an administrator what this server reports to Dagu Console. It lists
 * exactly what each choice sends, and what is never sent, before anything is.
 */
export function MonitoringDialog({
  open,
  level,
  prompt = false,
  onSave,
  onCancel,
}: {
  open: boolean;
  /** The level in effect; its choice starts selected when it reports. */
  level?: LicenseMonitoringLevel;
  /** Offers failure emails instead of presenting a setting. */
  prompt?: boolean;
  onSave: (level: ReportingLevel) => Promise<void>;
  onCancel: () => void;
}) {
  const { ts } = useI18n();
  const name = useId();
  const [selected, setSelected] = useState<ReportingLevel>(
    LicenseMonitoringLevel.runs
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reporting =
    level === LicenseMonitoringLevel.health ||
    level === LicenseMonitoringLevel.runs;

  useEffect(() => {
    if (!open) return;
    setSelected(
      level === LicenseMonitoringLevel.health
        ? LicenseMonitoringLevel.health
        : LicenseMonitoringLevel.runs
    );
    setError(null);
  }, [open, level]);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      await onSave(selected);
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not change what this server reports.')
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) onCancel();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>
            {ts(
              prompt
                ? 'Get an email when this server or its workflows fail'
                : 'Choose what this server reports'
            )}
          </DialogTitle>
          <DialogDescription>
            {ts(
              'Dagu Console emails you when this server stops reporting. With run status, it also emails you when a workflow fails.'
            )}
          </DialogDescription>
        </DialogHeader>
        <fieldset className="space-y-2" disabled={saving}>
          <legend className="sr-only">{ts('What to report')}</legend>
          {choices.map((choice) => {
            const id = `${name}-${choice.level}`;
            return (
              <label
                key={choice.level}
                htmlFor={id}
                className={cn(
                  'flex cursor-pointer items-start gap-3 rounded-md border p-3',
                  selected === choice.level
                    ? 'border-primary bg-primary/5'
                    : 'border-border'
                )}
              >
                <input
                  id={id}
                  type="radio"
                  name={name}
                  value={choice.level}
                  checked={selected === choice.level}
                  onChange={() => setSelected(choice.level)}
                  aria-labelledby={`${id}-title`}
                  aria-describedby={`${id}-sends`}
                  className="mt-1"
                />
                <span className="space-y-1">
                  <span
                    id={`${id}-title`}
                    className="block text-sm font-medium"
                  >
                    {ts(choice.title)}
                  </span>
                  <span
                    id={`${id}-sends`}
                    className="block text-sm text-muted-foreground"
                  >
                    {ts(choice.sends)}
                  </span>
                </span>
              </label>
            );
          })}
        </fieldset>
        <p className="text-sm text-muted-foreground">
          {ts(
            'Every report also carries what identifies and authenticates this server, as its license check-in does: the protocol version, license ID, server ID, and heartbeat secret. With run status, it also carries the reporter’s cursor and, after events were lost, the time they were lost from.'
          )}
        </p>
        <p className="text-sm text-muted-foreground">
          {ts(
            'Never sent: logs, outputs, parameters, environment, step commands, DAG YAML, the values of workflow secrets, and error message text.'
          )}
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <DialogFooter className="gap-2">
          <Button variant="ghost" disabled={saving} onClick={onCancel}>
            {ts(prompt ? 'Not now' : 'Cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={saving}
            onClick={() => void save()}
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {ts(reporting ? 'Save' : 'Turn on')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
