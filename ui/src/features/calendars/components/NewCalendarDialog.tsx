// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useClient } from '@/hooks/api';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import React, { useEffect, useState } from 'react';

const CALENDAR_NAME_PATTERN = /^[A-Za-z0-9_-]+$/;
const RESERVED_CALENDAR_NAMES = ['weekdays', 'none'];

type Props = {
  open: boolean;
  remoteNode: string;
  /** Names already taken, compared without case. */
  existingNames: string[];
  onClose: () => void;
  onCreated: (name: string) => void;
};

/**
 * Asks for the name of a new calendar file and creates it from the server's
 * template: Monday to Friday, with no holidays yet.
 */
export function NewCalendarDialog({
  open,
  remoteNode,
  existingNames,
  onClose,
  onCreated,
}: Props): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setName('');
      setError(null);
      setIsSaving(false);
    }
  }, [open]);

  function nameProblem(value: string): string | null {
    if (!CALENDAR_NAME_PATTERN.test(value)) {
      return ts('Use letters, digits, - and _ only.');
    }
    if (RESERVED_CALENDAR_NAMES.includes(value)) {
      return ts('{name} is reserved.', { name: value });
    }
    const lower = value.toLowerCase();
    if (existingNames.some((existing) => existing.toLowerCase() === lower)) {
      return ts('A calendar named {name} already exists.', { name: value });
    }
    return null;
  }

  async function handleSubmit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    const trimmed = name.trim();
    const problem = nameProblem(trimmed);
    if (problem) {
      setError(problem);
      return;
    }
    setIsSaving(true);
    try {
      const { error: apiError } = await client.POST('/calendars', {
        params: { query: { remoteNode } },
        body: { name: trimmed },
      });
      if (apiError) {
        setError(apiError.message || ts('Failed to create the calendar.'));
        return;
      }
      onCreated(trimmed);
    } catch {
      setError(ts('Failed to create the calendar.'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit} className="space-y-3">
          <DialogHeader>
            <DialogTitle>
              <I18nText text={'New calendar'} />
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5">
            <Label htmlFor="new-calendar-name">
              <I18nText text={'Name'} />
            </Label>
            <I18nProps>
              <Input
                id="new-calendar-name"
                value={name}
                placeholder="jp-banking"
                autoComplete="off"
                aria-invalid={!!error}
                aria-describedby="new-calendar-hint"
                onChange={(event) => {
                  setName(event.target.value);
                  setError(null);
                }}
              />
            </I18nProps>
            <p
              id="new-calendar-hint"
              className={
                error
                  ? 'text-xs text-destructive'
                  : 'text-xs text-muted-foreground'
              }
            >
              {error ?? (
                <I18nText
                  text={
                    'Schedules name it with calendar. It starts as Monday to Friday with no holidays.'
                  }
                />
              )}
            </p>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <I18nText text={'Cancel'} />
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={isSaving || !name.trim()}
            >
              <I18nText text={'Create'} />
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
