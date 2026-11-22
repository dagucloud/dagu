// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { JSONSchema } from '@/lib/schema-utils';
import DAGEditor from '../DAGEditor';

const { update, appSchema } = vi.hoisted(() => ({
  update: vi.fn<(options: unknown) => Promise<void>>(async () => undefined),
  appSchema: { current: null as JSONSchema | null },
}));

vi.mock('@monaco-editor/react', () => ({
  default: () => null,
  loader: { config: vi.fn() },
}));
vi.mock('monaco-editor', () => ({ editor: { setTheme: vi.fn() } }));
vi.mock('@/lib/monaco-theme', () => ({
  KILN_DARK: 'kiln-dark',
  KILN_LIGHT: 'kiln-light',
  registerKilnThemes: vi.fn(),
}));
vi.mock('@/lib/monaco-yaml', () => ({
  monacoYaml: { update, getOptions: () => ({}) },
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: appSchema.current }),
}));

type RegisteredSchema = {
  uri: string;
  schema?: JSONSchema;
  fileMatch: string[];
};

/** The schema that monaco-yaml last got for the model at `modelUri`. */
async function registeredFor(modelUri: string): Promise<RegisteredSchema> {
  let found: RegisteredSchema | undefined;
  await waitFor(() => {
    const options = update.mock.lastCall?.[0] as
      | { schemas: RegisteredSchema[] }
      | undefined;
    found = options?.schemas.find((item) => item.fileMatch[0] === modelUri);
    expect(found).toBeDefined();
  });
  return found!;
}

describe('DAGEditor schema', () => {
  beforeEach(() => {
    update.mockClear();
    appSchema.current = {
      type: 'object',
      properties: { create_branch: { const: true } },
    };
  });

  // Without a schema of its own, the editor checks the DAG schema that the
  // app loaded, in the form that monaco-yaml reads right.
  it('registers the DAG schema of the app when it has no schema', async () => {
    const modelUri = 'inmemory://dagu/test/no-schema.yaml';
    render(<DAGEditor value="" modelUri={modelUri} />);

    const { schema } = await registeredFor(modelUri);
    const flag = schema?.properties?.create_branch;
    expect(flag?.const).toBeUndefined();
    expect((flag?.allOf?.[0] as JSONSchema).errorMessage).toBe(
      'create_branch must be true.'
    );
  });

  it('registers its own schema over the DAG schema', async () => {
    const modelUri = 'inmemory://dagu/test/own-schema.yaml';
    const own: JSONSchema = {
      type: 'object',
      properties: { holidays: { type: 'array' } },
    };
    render(<DAGEditor value="" modelUri={modelUri} schema={own} />);

    const { schema } = await registeredFor(modelUri);
    expect(schema?.properties?.holidays).toBe(own.properties!.holidays);
    expect(schema?.properties?.create_branch).toBeUndefined();
  });
});
