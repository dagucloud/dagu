// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { dereferenceSchema, type JSONSchema } from '@/lib/schema-utils';
import { useEffect, useState } from 'react';

declare function getConfig(): { basePath: string; version: string };

// The schema is the same for every calendar, so it is fetched once.
let cachedSchema: JSONSchema | null = null;
let pendingSchema: Promise<JSONSchema> | null = null;

function loadCalendarSchema(): Promise<JSONSchema> {
  if (cachedSchema) {
    return Promise.resolve(cachedSchema);
  }
  if (!pendingSchema) {
    const { basePath, version } = getConfig();
    pendingSchema = fetch(
      `${basePath}/assets/calendar.schema.json?v=${version}`
    )
      .then((response) => {
        if (!response.ok) {
          throw new Error(
            `Failed to fetch the calendar schema: ${response.statusText}`
          );
        }
        return response.json();
      })
      .then((raw: JSONSchema) => {
        cachedSchema = dereferenceSchema(raw);
        return cachedSchema;
      })
      .catch((error: unknown) => {
        pendingSchema = null;
        throw error;
      });
  }
  return pendingSchema;
}

/**
 * Returns the resolved JSON schema of calendar files. It is null until it
 * loads, and an empty schema when it cannot, so the editor still opens.
 */
export function useCalendarSchema(): JSONSchema | null {
  const [schema, setSchema] = useState<JSONSchema | null>(cachedSchema);

  useEffect(() => {
    if (cachedSchema) {
      return;
    }
    let active = true;
    loadCalendarSchema()
      .then((loaded) => active && setSchema(loaded))
      .catch(() => active && setSchema({}));
    return () => {
      active = false;
    };
  }, []);

  return schema;
}
