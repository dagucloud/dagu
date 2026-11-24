// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { JSONSchema } from '@/lib/schema-utils';
import DAGEditor from '../DAGEditor';

const { update, appSchema, mountedEditor } = vi.hoisted(() => ({
  update: vi.fn<(options: unknown) => Promise<void>>(async () => undefined),
  appSchema: { current: null as JSONSchema | null },
  // The editor that the stand-in of Monaco mounts. Without one, the editor
  // never mounts and its skeleton stays.
  mountedEditor: { current: null as object | null },
}));

vi.mock('@monaco-editor/react', async () => {
  const { useEffect, useRef } = await import('react');
  return {
    default: function Editor({
      onMount,
    }: {
      onMount?: (editor: object) => void;
    }) {
      const onMountRef = useRef(onMount);
      useEffect(() => {
        if (mountedEditor.current) onMountRef.current?.(mountedEditor.current);
      }, []);
      return null;
    },
    loader: { config: vi.fn() },
  };
});
vi.mock('monaco-editor', () => ({
  editor: { setTheme: vi.fn(), getModel: () => null },
  Uri: { parse: (uri: string) => uri },
  Emitter: class {
    event = vi.fn();
    fire = vi.fn();
    dispose = vi.fn();
  },
  languages: { registerCodeLensProvider: () => ({ dispose: vi.fn() }) },
}));
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

describe('DAGEditor skeleton', () => {
  beforeEach(() => {
    // No DOM node: the editor counts as settled as soon as it mounts.
    mountedEditor.current = {
      getDomNode: () => null,
      getModel: () => null,
      onKeyDown: vi.fn(),
      addCommand: vi.fn(() => 'cmd-1'),
    };
  });
  afterEach(() => {
    mountedEditor.current = null;
  });

  // The steps come from a parse of the text. Once the editor has taken the
  // place of its skeleton, only Monaco's lens queries read them: an edit
  // costs no parse for the skeleton.
  it('reads no steps for the skeleton once the editor shows', async () => {
    const steps = vi.fn(() => [{ line: 2, name: 'a' }]);
    const lenses = { label: 'Open step', open: vi.fn(), steps };
    const { rerender } = render(
      <DAGEditor value={'steps:\n  - id: a'} readOnly stepLenses={lenses} />
    );
    expect(steps).toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.queryByTestId('editor-skeleton')).toBeNull()
    );

    steps.mockClear();
    rerender(
      <DAGEditor value={'steps:\n  - id: ab'} readOnly stepLenses={lenses} />
    );
    expect(steps).not.toHaveBeenCalled();
  });
});
