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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useState } from 'react';
import {
  createInventory,
  recordOf,
  replaceInventory,
  type ChangeRefusal,
  type Inventory,
  type InventoryDefaults,
  type InventoryRecord,
} from './inventoryChanges';
import { RefusalNotice } from './RefusalNotice';

/** The fields the form shows. Other defaults are kept as they are. */
type Fields = {
  name: string;
  description: string;
  user: string;
  port: string;
  keyFile: string;
  keySecret: string;
  passwordSecret: string;
  passphraseSecret: string;
  labels: string;
  strictHostKey: boolean;
};

/**
 * Creates an inventory, or changes the name, description, and defaults of
 * one. Defaults the form does not show, such as `bastion` and `shell`, and
 * the nodes are kept; the JSON editor changes those.
 */
export function InventoryDialog({
  inventory,
  onClose,
  onSaved,
}: {
  /** The inventory to change, or undefined to create one. */
  inventory?: Inventory;
  onClose: () => void;
  onSaved: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [base, setBase] = useState(inventory);
  const [fields, setFields] = useState(() => fieldsOf(inventory));
  const [problem, setProblem] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  const [saving, setSaving] = useState(false);

  const set = (name: keyof Fields) => (value: string | boolean) =>
    setFields((prev) => ({ ...prev, [name]: value }));

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    const built = buildRecord(base ? recordOf(base) : undefined, fields);
    if (typeof built === 'string') {
      setProblem(ts(built));
      return;
    }
    setProblem(null);
    setSaving(true);
    try {
      const { refusal } = base
        ? await replaceInventory(client, base.id, base.version, built)
        : await createInventory(client, built);
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
      <DialogContent className="sm:max-w-[560px]">
        <form onSubmit={(event) => void submit(event)} className="space-y-4">
          <DialogHeader>
            <DialogTitle>
              {base ? (
                <I18nText text={'Edit inventory'} />
              ) : (
                <I18nText text={'New inventory'} />
              )}
            </DialogTitle>
            <DialogDescription>
              <I18nText
                text={
                  'Defaults fill the fields a node leaves unset. Credentials are key paths and secret refs, never values.'
                }
              />
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name" className="sm:col-span-2">
              <Input
                required
                maxLength={200}
                value={fields.name}
                onChange={(e) => set('name')(e.target.value)}
              />
            </Field>
            <Field label="Description" className="sm:col-span-2">
              <Input
                value={fields.description}
                onChange={(e) => set('description')(e.target.value)}
              />
            </Field>
            <Field label="Default user">
              <Input
                value={fields.user}
                onChange={(e) => set('user')(e.target.value)}
              />
            </Field>
            <Field label="Default port">
              <Input
                inputMode="numeric"
                placeholder="22"
                value={fields.port}
                onChange={(e) => set('port')(e.target.value)}
              />
            </Field>
            <Field label="Key file">
              <Input
                placeholder="~/.ssh/id_ed25519"
                value={fields.keyFile}
                onChange={(e) => set('keyFile')(e.target.value)}
              />
            </Field>
            <Field label="Key secret">
              <Input
                placeholder="ssh/fleet"
                value={fields.keySecret}
                onChange={(e) => set('keySecret')(e.target.value)}
              />
            </Field>
            <Field label="Password secret">
              <Input
                value={fields.passwordSecret}
                onChange={(e) => set('passwordSecret')(e.target.value)}
              />
            </Field>
            <Field label="Passphrase secret">
              <Input
                value={fields.passphraseSecret}
                onChange={(e) => set('passphraseSecret')(e.target.value)}
              />
            </Field>
            <Field label="Default labels" className="sm:col-span-2">
              <Input
                placeholder="site=tokyo, env=prod"
                value={fields.labels}
                onChange={(e) => set('labels')(e.target.value)}
              />
            </Field>
            <label className="flex items-center gap-2 text-xs sm:col-span-2">
              <Checkbox
                checked={fields.strictHostKey}
                onCheckedChange={(value) =>
                  set('strictHostKey')(value === true)
                }
              />
              <I18nText
                text={'Refuse host keys that are neither pinned nor trusted'}
              />
            </label>
          </div>
          {problem && (
            <p role="alert" className="text-xs text-error">
              {problem}
            </p>
          )}
          {refusal && (
            <RefusalNotice
              refusal={refusal}
              onLoadCurrent={(current) => {
                setBase(current);
                setFields(fieldsOf(current));
                setRefusal(null);
              }}
            />
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={saving}>
              {base ? <I18nText text={'Save'} /> : <I18nText text={'Create'} />}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: React.ReactElement;
}): React.ReactElement {
  const id = `inventory-${label.toLowerCase().replace(/\s+/g, '-')}`;
  return (
    <div className={className}>
      <Label htmlFor={id} className="mb-1 block text-xs">
        <I18nText text={label} />
      </Label>
      {React.cloneElement(children as React.ReactElement<{ id?: string }>, {
        id,
      })}
    </div>
  );
}

function fieldsOf(inventory?: Inventory): Fields {
  const defaults = inventory?.defaults ?? {};
  const key = credentialOf(defaults.key);
  return {
    name: inventory?.name ?? '',
    description: inventory?.description ?? '',
    user: defaults.user ?? '',
    port: defaults.port ? String(defaults.port) : '',
    keyFile: key.file,
    keySecret: key.secret,
    passwordSecret: credentialOf(defaults.password).secret,
    passphraseSecret: credentialOf(defaults.passphrase).secret,
    labels: (defaults.labels ?? []).join(', '),
    strictHostKey: defaults.strict_host_key !== false,
  };
}

/** A credential's key path, or its secret ref. */
function credentialOf(value: unknown): { file: string; secret: string } {
  if (typeof value === 'string') {
    return { file: value, secret: '' };
  }
  const credential = (value ?? {}) as { file?: string; secret?: string };
  return { file: credential.file ?? '', secret: credential.secret ?? '' };
}

/**
 * The record with the form's fields applied over base, or why the fields
 * cannot make one. A credential whose text did not change keeps its form.
 */
function buildRecord(
  base: InventoryRecord | undefined,
  fields: Fields
): InventoryRecord | string {
  const before = base?.defaults ?? {};
  const defaults: InventoryDefaults = { ...before };
  const setOrDrop = <K extends keyof InventoryDefaults>(
    key: K,
    value: InventoryDefaults[K] | undefined
  ) => {
    if (value === undefined) {
      delete defaults[key];
    } else {
      defaults[key] = value;
    }
  };

  const port = fields.port.trim();
  if (port && !/^\d+$/.test(port)) {
    return 'The default port must be a whole number.';
  }
  const keyFile = fields.keyFile.trim();
  const keySecret = fields.keySecret.trim();
  if (keyFile && keySecret) {
    return 'Give a key file or a key secret, not both.';
  }
  const keyBefore = credentialOf(before.key);
  const sameKey = keyFile === keyBefore.file && keySecret === keyBefore.secret;
  const secretRef = (ref: string) =>
    ref.trim() ? { secret: ref.trim() } : undefined;
  const labels = fields.labels
    .split(',')
    .map((label) => label.trim())
    .filter(Boolean);

  setOrDrop('user', fields.user.trim() || undefined);
  setOrDrop('port', port ? Number(port) : undefined);
  setOrDrop(
    'key',
    sameKey
      ? before.key
      : keyFile || (keySecret ? { secret: keySecret } : undefined)
  );
  setOrDrop('password', secretRef(fields.passwordSecret));
  setOrDrop('passphrase', secretRef(fields.passphraseSecret));
  setOrDrop('labels', labels.length > 0 ? labels : undefined);
  setOrDrop(
    'strict_host_key',
    fields.strictHostKey
      ? before.strict_host_key === true
        ? true
        : undefined
      : false
  );

  const record: InventoryRecord = {
    name: fields.name.trim(),
    nodes: base?.nodes ?? [],
  };
  if (fields.description.trim()) {
    record.description = fields.description.trim();
  }
  if (Object.keys(defaults).length > 0) {
    record.defaults = defaults;
  }
  return record;
}
