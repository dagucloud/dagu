// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import type { JSONSchema as MonacoJSONSchema } from 'monaco-yaml';

export type SchemaRegistration = {
  fileMatch: string;
  uri: string;
  schema?: MonacoJSONSchema;
};

export type SchemaRegistrationOwner = {
  ownerId: string;
  modelUri: string;
  registration: SchemaRegistration;
  fingerprint: string;
};

export type StoredSchemaRegistration = {
  modelUri: string;
  owners: SchemaRegistrationOwner[];
  registration: SchemaRegistration;
  fingerprint: string;
};

type SchemaRegistrationStore = Map<string, StoredSchemaRegistration>;

function normalizeForFingerprint(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => normalizeForFingerprint(item));
  }
  if (typeof value !== 'object' || value === null) {
    return value;
  }

  const record = value as Record<string, unknown>;
  const normalized: Record<string, unknown> = {};
  for (const key of Object.keys(record).sort()) {
    const item = normalizeForFingerprint(record[key]);
    if (item !== undefined) {
      normalized[key] = item;
    }
  }
  return normalized;
}

function stableStringify(value: unknown): string {
  return JSON.stringify(normalizeForFingerprint(value));
}

export function getDocumentSchemaUri(modelUri: string): string {
  const stableId = modelUri
    .replace(/^[A-Za-z][A-Za-z0-9+.-]*:\/\//, '')
    .replace(/[^A-Za-z0-9._-]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return `inmemory://dagu-schema/${stableId || 'document'}.schema.json`;
}

export function buildSchemaRegistration(
  ownerId: string,
  modelUri: string,
  schema: JSONSchema | null | undefined,
  defaultSchemaUrl: string
): SchemaRegistrationOwner {
  const documentSchemaUri = getDocumentSchemaUri(modelUri);
  const registration: SchemaRegistration = {
    fileMatch: modelUri,
    uri: schema ? documentSchemaUri : defaultSchemaUrl,
    schema: schema
      ? ({
          ...schema,
          $id: documentSchemaUri,
        } as MonacoJSONSchema)
      : undefined,
  };

  return {
    ownerId,
    modelUri,
    registration,
    fingerprint: stableStringify(registration),
  };
}

function storeEntryFromOwners(
  modelUri: string,
  owners: SchemaRegistrationOwner[]
): StoredSchemaRegistration {
  const activeOwner = owners[owners.length - 1];
  if (!activeOwner) {
    throw new Error('schema registration owner list is empty');
  }

  return {
    modelUri,
    owners,
    registration: activeOwner.registration,
    fingerprint: activeOwner.fingerprint,
  };
}

export function upsertSchemaRegistration(
  registrations: SchemaRegistrationStore,
  next: SchemaRegistrationOwner
): boolean {
  const current = registrations.get(next.modelUri);
  if (!current) {
    registrations.set(
      next.modelUri,
      storeEntryFromOwners(next.modelUri, [next])
    );
    return true;
  }

  const previousFingerprint = current.fingerprint;
  const owners = current.owners.filter(
    (owner) => owner.ownerId !== next.ownerId
  );
  owners.push(next);
  registrations.set(next.modelUri, storeEntryFromOwners(next.modelUri, owners));
  return previousFingerprint !== next.fingerprint;
}

export function removeSchemaRegistration(
  registrations: SchemaRegistrationStore,
  modelUri: string,
  ownerId: string,
  expectedFingerprint?: string
): boolean {
  const current = registrations.get(modelUri);
  if (!current) {
    return false;
  }
  const owner = current.owners.find((item) => item.ownerId === ownerId);
  if (!owner) {
    return false;
  }
  if (expectedFingerprint && owner.fingerprint !== expectedFingerprint) {
    return false;
  }

  const previousFingerprint = current.fingerprint;
  const owners = current.owners.filter((item) => item.ownerId !== ownerId);
  if (owners.length === 0) {
    registrations.delete(modelUri);
    return true;
  }

  const next = storeEntryFromOwners(modelUri, owners);
  registrations.set(modelUri, next);
  return previousFingerprint !== next.fingerprint;
}

export function toMonacoYamlSchemas(registrations: SchemaRegistrationStore) {
  return Array.from(registrations.values()).map(({ registration }) => ({
    uri: registration.uri,
    schema: registration.schema,
    fileMatch: [registration.fileMatch],
  }));
}
