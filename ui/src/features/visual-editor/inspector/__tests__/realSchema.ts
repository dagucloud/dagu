// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { dereferenceSchema, type JSONSchema } from '@/lib/schema-utils';

const schemaPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../../../../schemas/dag.schema.json'
);

/**
 * The dereferenced repository DAG schema, or null when the test runs
 * without the repository root (for example in a copy of `ui/` only).
 */
export const realSchema: JSONSchema | null = fs.existsSync(schemaPath)
  ? dereferenceSchema(
      JSON.parse(fs.readFileSync(schemaPath, 'utf8')) as JSONSchema
    )
  : null;
