// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import { useClient } from '@/hooks/api';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useState } from 'react';
import {
  importInventories,
  type ChangeRefusal,
  type ImportResult,
} from './inventoryChanges';
import { RefusalNotice } from './RefusalNotice';

/**
 * Imports inventories from the JSON that `dagu nodes export` prints, as
 * `dagu nodes import` does. The import is checked first, and Import stores
 * it only after a check passes for the text and options as they are now.
 */
export function ImportDialog({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [text, setText] = useState('');
  const [replace, setReplace] = useState(false);
  // A check that passed, and the text and options it checked.
  const [checked, setChecked] = useState<{
    input: string;
    result: ImportResult;
  } | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  const [busy, setBusy] = useState(false);

  const input = JSON.stringify([text, replace]);
  const check = checked?.input === input ? checked.result : null;
  const empty = !!check && check.created.length + check.replaced.length === 0;

  async function run(dryRun: boolean) {
    const records = parseRecords(text);
    if (typeof records === 'string') {
      setProblem(ts(records));
      return;
    }
    setProblem(null);
    setRefusal(null);
    setBusy(true);
    try {
      const { result, refusal } = await importInventories(client, records, {
        replace,
        dryRun,
      });
      if (!result) {
        setChecked(null);
        setRefusal(refusal ?? null);
        return;
      }
      if (dryRun) {
        setChecked({ input, result });
        return;
      }
      onImported();
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[640px]">
        <DialogHeader>
          <DialogTitle>
            <I18nText text={'Import inventories'} />
          </DialogTitle>
          <DialogDescription>
            <I18nText
              text={
                'Paste the JSON that dagu nodes export prints, or choose a file. The import is checked before it is stored.'
              }
            />
          </DialogDescription>
        </DialogHeader>
        <I18nProps>
          <Textarea
            aria-label="Inventories JSON"
            className="h-64 font-mono text-xs"
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
        </I18nProps>
        <div className="flex flex-wrap items-center gap-4 text-xs">
          <label className="flex items-center gap-2">
            <span className="text-muted-foreground">
              <I18nText text={'Choose file'} />
            </span>
            <input
              type="file"
              accept=".json,application/json"
              onChange={async (event) => {
                const file = event.target.files?.[0];
                if (file) {
                  setText(await file.text());
                }
              }}
            />
          </label>
          <label className="flex items-center gap-2">
            <Checkbox
              checked={replace}
              onCheckedChange={(value) => setReplace(value === true)}
            />
            <I18nText text={'Replace inventories with the same name'} />
          </label>
        </div>
        {check && (
          <div aria-live="polite" className="space-y-1 text-xs">
            {empty && <I18nText text={'Nothing to import.'} />}
            {check.created.length > 0 && (
              <p>
                <I18nTemplate
                  text="Would create: {names}"
                  values={{ names: check.created.join(', ') }}
                />
              </p>
            )}
            {check.replaced.length > 0 && (
              <p>
                <I18nTemplate
                  text="Would replace: {names}"
                  values={{ names: check.replaced.join(', ') }}
                />
              </p>
            )}
          </div>
        )}
        {problem && (
          <p role="alert" className="text-xs text-error">
            {problem}
          </p>
        )}
        {refusal && (
          <RefusalNotice refusal={refusal} onLoadCurrent={() => {}} />
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => void run(true)}
          >
            <I18nText text={'Check'} />
          </Button>
          <Button
            type="button"
            disabled={busy || !check || empty}
            onClick={() => void run(false)}
          >
            <I18nText text={'Import'} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The inventories in text, or why text holds none. */
function parseRecords(text: string): unknown[] | string {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return 'The text is not valid JSON.';
  }
  if (!Array.isArray(value)) {
    return 'An import is a JSON array of inventories, as dagu nodes export prints it.';
  }
  return value;
}
