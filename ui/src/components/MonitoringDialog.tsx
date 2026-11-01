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

/** A level at which this server reports to Dagu Console. */
export type ReportingLevel =
  | LicenseMonitoringLevel.health
  | LicenseMonitoringLevel.runs
  | LicenseMonitoringLevel.errors;

/** What each level sends, worded as RFC 002 lists it. */
const choices: { level: ReportingLevel; title: string; sends: string }[] = [
  {
    level: LicenseMonitoringLevel.health,
    title: 'Health only',
    sends:
      "Sends the Dagu version, OS, architecture, process start time, service counts (schedulers holding the scheduler lock, and registered coordinators), the scheduler's last tick and whether it is paused, and how far remote access lets calls from Dagu Cloud go.",
  },
  {
    level: LicenseMonitoringLevel.runs,
    title: 'Health and run status',
    sends:
      "Sends the health fields, plus the status of each top-level DAG run that finishes, with its failed step names (up to 50) and how it was triggered, and the list of runs queued, running, or waiting. Each run is sent as its DAG name, run ID, attempt ID, status, trigger, and queued, started, and finished times. Also each scheduled DAG's next run, and whether its latest scheduled run started. No parameters, logs, or outputs.",
  },
  {
    level: LicenseMonitoringLevel.errors,
    title: 'Health, run status, and error messages (recommended)',
    sends:
      "Sends everything Health and run status sends, plus why each failed run failed: the error messages of up to 5 failed steps and handlers, or the run's own, each up to 2 KB from its end. A step's error can end with the last lines of its stderr, with the values of workflow secrets masked.",
  },
];

/** What no report sends, at any level. */
export const neverSent =
  'Never sent: full logs, outputs, parameters, environment, step commands, DAG YAML, schedule expressions, and the values of workflow secrets.';

/** Lets an administrator pick a level, listing what each one sends. */
export function ReportingChoices({
  selected,
  onSelect,
  disabled = false,
  className,
}: {
  selected: ReportingLevel;
  onSelect: (level: ReportingLevel) => void;
  disabled?: boolean;
  className?: string;
}) {
  const { ts } = useI18n();
  const name = useId();
  return (
    <fieldset className={cn('space-y-2', className)} disabled={disabled}>
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
              onChange={() => onSelect(choice.level)}
              aria-labelledby={`${id}-title`}
              aria-describedby={`${id}-sends`}
              className="mt-1"
            />
            <span className="space-y-1">
              <span id={`${id}-title`} className="block text-sm font-medium">
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
  );
}

/**
 * Asks an administrator what this server reports to Dagu Console. It lists
 * exactly what each choice sends, and what is never sent, before anything is.
 */
export function MonitoringDialog({
  open,
  level,
  suggested,
  prompt = false,
  onSave,
  onCancel,
}: {
  open: boolean;
  /** The level in effect; its choice starts selected when it reports. */
  level?: LicenseMonitoringLevel;
  /** The choice that starts selected instead of the level in effect. */
  suggested?: ReportingLevel;
  /** Offers failure emails instead of presenting a setting. */
  prompt?: boolean;
  onSave: (level: ReportingLevel) => Promise<void>;
  onCancel: () => void;
}) {
  const { ts } = useI18n();
  const [selected, setSelected] = useState<ReportingLevel>(
    LicenseMonitoringLevel.errors
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reporting = level !== undefined && level !== LicenseMonitoringLevel.off;

  useEffect(() => {
    if (!open) return;
    setSelected(
      suggested ??
        (level === undefined || level === LicenseMonitoringLevel.off
          ? LicenseMonitoringLevel.errors
          : level)
    );
    setError(null);
  }, [open, level, suggested]);

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
                ? 'Monitor this server from Dagu Console'
                : 'Choose what this server reports'
            )}
          </DialogTitle>
          <DialogDescription>
            {ts(
              "Dagu Console shows this server's health on its Servers page. With run status, it also emails the workspace's owners and admins when a workflow fails."
            )}
          </DialogDescription>
        </DialogHeader>
        <ReportingChoices
          selected={selected}
          onSelect={setSelected}
          disabled={saving}
        />
        <p className="text-sm text-muted-foreground">
          {ts(
            'Every report also carries what identifies and authenticates this server, as its license check-in does: the protocol version, license ID, server ID, and heartbeat secret, and which choice it was sent under. With run status, it also carries the reporter’s cursor and, after events were lost, the time they were lost from. Turning reporting off sends one last report that carries only what identifies and authenticates this server and the choice Off, so that Dagu Console shows monitoring as off.'
          )}
        </p>
        <p className="text-sm text-muted-foreground">{ts(neverSent)}</p>
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
