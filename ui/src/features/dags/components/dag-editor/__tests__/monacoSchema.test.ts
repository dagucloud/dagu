// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import { describe, expect, it } from 'vitest';
import { realSchema } from '@/features/visual-editor/inspector/__tests__/realSchema';
import { forMonacoYaml } from '../monacoSchema';

const TRUE_TEXTS = [true, 'true', 'True', 'TRUE'];
const FALSE_TEXTS = [false, 'false', 'False', 'FALSE'];

/** The schema that the language service reads for `{ const: true }` at `key`. */
function ruleOf(schema: JSONSchema, key: string) {
  const converted = forMonacoYaml(schema);
  return (converted.properties?.[key]?.allOf as JSONSchema[])[0]!;
}

describe('forMonacoYaml', () => {
  it('replaces a boolean const by a rule on the type and the other boolean', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        read: { const: true, description: 'Read files.' },
      },
    };
    const converted = forMonacoYaml(schema);
    const read = converted.properties!.read!;
    expect(read.const).toBeUndefined();
    expect(read.description).toBe('Read files.');
    const rule = ruleOf(schema, 'read');
    expect(rule.type).toBe('boolean');
    // The condition is met by false, in every spelling, and then it fails.
    expect(rule.if?.anyOf).toEqual(
      FALSE_TEXTS.map((text) => ({ const: text }))
    );
    expect(rule.then).toEqual({
      enum: [],
      errorMessage: 'read must be true.',
    });
    expect(rule.errorMessage).toBe('read must be true.');
  });

  it('turns const false the same way, with the texts of true in the condition', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: { off: { const: false } },
    };
    const rule = ruleOf(schema, 'off');
    expect(rule.if?.anyOf).toEqual(TRUE_TEXTS.map((text) => ({ const: text })));
    expect(rule.errorMessage).toBe('off must be false.');
  });

  it('says only the value when the rule has no key', () => {
    const converted = forMonacoYaml({
      type: 'object',
      additionalProperties: { const: false },
    });
    const rule = (converted.additionalProperties as JSONSchema)
      .allOf![0] as JSONSchema;
    expect(rule.errorMessage).toBe('Value must be false.');
  });

  it('keeps the rules that are already in allOf', () => {
    const existing: JSONSchema = { type: 'boolean' };
    const converted = forMonacoYaml({
      type: 'object',
      properties: { read: { const: true, allOf: [existing] } },
    });
    const allOf = converted.properties!.read!.allOf!;
    expect(allOf).toHaveLength(2);
    expect(allOf[0]).toBe(existing);
  });

  it('adds the texts of each boolean in an enum, and keeps the boolean', () => {
    const converted = forMonacoYaml({
      type: 'object',
      properties: { mode: { enum: ['auto', true, 3, true] } },
    });
    expect(converted.properties!.mode!.enum).toEqual([
      'auto',
      ...TRUE_TEXTS,
      3,
    ]);
  });

  it('leaves the other const and enum rules alone', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        action: { const: 'agent.run' },
        count: { const: 3 },
        kind: { enum: ['a', 'b'] },
        // A property that is named like a keyword.
        const: { type: 'string' },
        enum: { type: 'string' },
      },
    };
    expect(forMonacoYaml(schema)).toBe(schema);
  });

  it('does not read a default, an example, a const or an enum as a schema', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        flags: {
          type: 'object',
          default: { const: true },
          examples: [{ const: true }],
        },
        pick: { enum: [{ const: true }] },
        value: { const: { const: true } },
      },
    };
    expect(forMonacoYaml(schema)).toBe(schema);
  });

  it('does not change the input, and returns the same result for it again', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: { read: { const: true } },
    };
    const before = JSON.stringify(schema);
    const converted = forMonacoYaml(schema);
    expect(converted).not.toBe(schema);
    expect(JSON.stringify(schema)).toBe(before);
    expect(forMonacoYaml(schema)).toBe(converted);
  });

  it('finds a rule inside if, then, allOf and a list of schemas', () => {
    const converted = forMonacoYaml({
      allOf: [
        {
          if: { properties: { delete_branch: { const: true } } },
          then: { properties: { force: { const: true } } },
        },
      ],
      oneOf: [{ properties: { x: { const: false } } }],
    });
    const branch = converted.allOf![0] as JSONSchema;
    expect(branch.if!.properties!.delete_branch!.const).toBeUndefined();
    expect(branch.then!.properties!.force!.allOf).toHaveLength(1);
    expect(converted.oneOf![0]!.properties!.x!.allOf).toHaveLength(1);
  });

  it('converts a part that two places share once, and names the key where it needs to', () => {
    const shared: JSONSchema = {
      type: 'object',
      properties: { read: { const: true } },
    };
    const converted = forMonacoYaml({
      type: 'object',
      properties: { a: shared, b: shared },
      items: shared,
    });
    expect(converted.properties!.a).toBe(converted.properties!.b);
    expect(converted.items).toBe(converted.properties!.a);
    const read = (converted.properties!.a as JSONSchema).properties!.read!;
    expect((read.allOf![0] as JSONSchema).errorMessage).toBe(
      'read must be true.'
    );
  });

  it('keeps a part that a name gives its message to apart from the same part without a name', () => {
    const flag: JSONSchema = { const: true };
    const converted = forMonacoYaml({
      type: 'object',
      properties: { read: flag },
      additionalProperties: flag,
    });
    const named = converted.properties!.read!.allOf![0] as JSONSchema;
    const open = (converted.additionalProperties as JSONSchema)
      .allOf![0] as JSONSchema;
    expect(named.errorMessage).toBe('read must be true.');
    expect(open.errorMessage).toBe('Value must be true.');
  });

  it('does not loop on a schema that refers to itself', () => {
    const schema: JSONSchema = { type: 'object', properties: {} };
    schema.properties!.self = schema;
    schema.properties!.read = { const: true };
    const converted = forMonacoYaml(schema);
    expect(converted.properties!.read!.const).toBeUndefined();
  });

  it.skipIf(!realSchema)(
    'gives the keys of the built-in tools of the real schema a rule',
    () => {
      const tools = forMonacoYaml(realSchema!).definitions!
        .agentRunBuiltinTools!;
      for (const key of ['read', 'write', 'edit', 'search']) {
        const property = tools.properties![key]!;
        expect(property.const).toBeUndefined();
        expect(property.description).toEqual(expect.any(String));
        expect((property.allOf![0] as JSONSchema).errorMessage).toBe(
          `${key} must be true.`
        );
      }
      // The shell key is an object: it does not change.
      expect(tools.properties!.shell).toBe(
        realSchema!.definitions!.agentRunBuiltinTools!.properties!.shell
      );
    }
  );

  it.skipIf(!realSchema)(
    'does not change the part of the real schema that has no boolean const',
    () => {
      const converted = forMonacoYaml(realSchema!);
      const original = realSchema!.definitions!;
      const result = converted.definitions!;
      expect(result.httpRequestActionConfig).toBe(
        original.httpRequestActionConfig
      );
    }
  );
});
