// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import type { JSONSchema as MonacoJSONSchema } from 'monaco-yaml';

export type SchemaRegistration = {
  fileMatch: string;
  uri: string;
  schema?: MonacoJSONSchema;
};

export type StoredSchemaRegistration = {
  modelUri: string;
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
  modelUri: string,
  schema: JSONSchema | null | undefined,
  defaultSchemaUrl: string
): StoredSchemaRegistration {
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
    modelUri,
    registration,
    fingerprint: stableStringify(registration),
  };
}

export function upsertSchemaRegistration(
  registrations: SchemaRegistrationStore,
  next: StoredSchemaRegistration
): boolean {
  const current = registrations.get(next.modelUri);
  if (current?.fingerprint === next.fingerprint) {
    return false;
  }

  registrations.set(next.modelUri, next);
  return true;
}

export function removeSchemaRegistration(
  registrations: SchemaRegistrationStore,
  modelUri: string,
  expectedFingerprint?: string
): boolean {
  const current = registrations.get(modelUri);
  if (!current) {
    return false;
  }
  if (expectedFingerprint && current.fingerprint !== expectedFingerprint) {
    return false;
  }

  registrations.delete(modelUri);
  return true;
}

export function toMonacoYamlSchemas(registrations: SchemaRegistrationStore) {
  return Array.from(registrations.values()).map(({ registration }) => ({
    uri: registration.uri,
    schema: registration.schema,
    fileMatch: [registration.fileMatch],
  }));
}
