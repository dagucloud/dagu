// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useClient } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import MonacoEditor, { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import React, { useEffect, useState } from 'react';
import {
  recordOf,
  replaceInventory,
  type ChangeRefusal,
  type Inventory,
  type InventoryRecord,
} from './inventoryChanges';
import { RefusalNotice } from './RefusalNotice';

loader.config({ monaco });

declare function getConfig(): { basePath: string; version: string };

// The model the schema applies to.
const MODEL_URI = 'inmemory://dagu/inventory.json';

// The schema is the same for every inventory, so it is fetched once.
let schemaRequest: Promise<unknown> | null = null;

function loadSchema(): Promise<unknown> {
  if (!schemaRequest) {
    const { basePath, version } = getConfig();
    schemaRequest = fetch(
      `${basePath}/assets/inventory.schema.json?v=${version}`
    )
      .then((response) => {
        if (!response.ok) {
          throw new Error(response.statusText);
        }
        return response.json();
      })
      .catch((error: unknown) => {
        schemaRequest = null;
        throw error;
      });
  }
  return schemaRequest;
}

/**
 * Edits an inventory as the JSON that `dagu nodes export` prints, checked
 * against the inventory schema as the user types. A change that is not
 * JSON, or breaks the schema, cannot be saved; the server checks the rest.
 */
export function InventoryJSONDialog({
  inventory,
  onClose,
  onSaved,
}: {
  inventory: Inventory;
  onClose: () => void;
  onSaved: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [base, setBase] = useState(inventory);
  const [text, setText] = useState(() => toJSON(inventory));
  const [schemaErrors, setSchemaErrors] = useState<string[]>([]);
  const [schema, setSchema] = useState<unknown>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let active = true;
    loadSchema()
      .then((loaded) => active && setSchema(loaded))
      .catch(() => {
        // Without the schema the editor checks JSON syntax only, and the
        // server still checks every rule.
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!schema) {
      return;
    }
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      allowComments: false,
      // Monaco reports schema violations as warnings by default, which
      // would not block Save.
      schemaValidation: 'error',
      schemas: [
        {
          uri: 'dagu://schemas/inventory.json',
          fileMatch: [MODEL_URI],
          schema,
        },
      ],
    });
  }, [schema]);

  const parsed = parseRecord(text);
  const problems = typeof parsed === 'string' ? [ts(parsed)] : schemaErrors;

  async function save() {
    if (typeof parsed === 'string' || problems.length > 0) {
      return;
    }
    setSaving(true);
    try {
      const { refusal } = await replaceInventory(
        client,
        base.id,
        base.version,
        parsed
      );
      if (refusal) {
        setRefusal(refusal);
        return;
      }
      onSaved();
      onClose();
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[85dvh] max-w-none flex-col sm:w-[min(56rem,calc(100vw-2rem))]">
        <DialogHeader>
          <DialogTitle>
            <I18nTemplate
              text="Edit {name} as JSON"
              values={{ name: base.name }}
            />
          </DialogTitle>
          <DialogDescription>
            <I18nText
              text={
                'The record that dagu nodes export prints. Removing or renaming a node forgets its trusted host key.'
              }
            />
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-hidden rounded border border-border">
          <MonacoEditor
            path={MODEL_URI}
            language="json"
            value={text}
            onChange={(value) => setText(value ?? '')}
            onValidate={(markers) =>
              setSchemaErrors(
                markers
                  .filter(
                    (marker) => marker.severity === monaco.MarkerSeverity.Error
                  )
                  .map(
                    (marker) =>
                      `${marker.startLineNumber}:${marker.startColumn} ${marker.message}`
                  )
              )
            }
            options={{
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              tabSize: 2,
              ariaLabel: ts('Inventory JSON'),
            }}
          />
        </div>
        {problems.length > 0 && (
          <ul
            role="alert"
            className="max-h-24 space-y-0.5 overflow-y-auto font-mono text-xs text-error"
          >
            {problems.map((problem) => (
              <li key={problem}>{problem}</li>
            ))}
          </ul>
        )}
        {refusal && (
          <RefusalNotice
            refusal={refusal}
            onLoadCurrent={(current) => {
              setBase(current);
              setText(toJSON(current));
              setRefusal(null);
            }}
          />
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            type="button"
            disabled={saving || problems.length > 0}
            onClick={() => void save()}
          >
            <I18nText text={'Save'} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function toJSON(inventory: Inventory): string {
  return `${JSON.stringify(recordOf(inventory), null, 2)}\n`;
}

/** The record in text, or why text is not one. */
function parseRecord(text: string): InventoryRecord | string {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return 'The text is not valid JSON.';
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return 'An inventory is one JSON object.';
  }
  return value as InventoryRecord;
}
