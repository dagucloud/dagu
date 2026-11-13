// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { ValidationIssue } from '../../contracts';
import {
  chatIssueCounts,
  LLM_PROVIDERS,
  otherChatKeys,
  parseNumberText,
  readMessages,
  takesBaseUrl,
} from '../sections/chat/model';

const issue = (message: string): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message,
});

const schemaPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../../../../internal/cmn/schema/dag.schema.json'
);

describe('LLM_PROVIDERS', () => {
  // Skipped in a copy of `ui/` without the repository root.
  it.skipIf(!existsSync(schemaPath))(
    'lists the providers of the DAG schema',
    () => {
      const schema = JSON.parse(readFileSync(schemaPath, 'utf8')) as {
        definitions: {
          llmProvider: { anyOf: [{ enum: string[] }, unknown] };
        };
      };
      expect([...LLM_PROVIDERS]).toEqual(
        schema.definitions.llmProvider.anyOf[0].enum
      );
    }
  );
});

describe('takesBaseUrl', () => {
  it('is true for the providers that the user runs', () => {
    expect(takesBaseUrl('local')).toBe(true);
    expect(takesBaseUrl('ollama')).toBe(true);
    expect(takesBaseUrl('openai')).toBe(false);
    expect(takesBaseUrl(undefined)).toBe(false);
  });
});

describe('parseNumberText', () => {
  const int = { integer: true, min: 1 };
  const unit = { integer: false, min: 0, max: 1 };

  it('removes the key for an empty text', () => {
    expect(parseNumberText('  ', int)).toEqual({ ok: true, value: undefined });
  });

  it('reads a whole number and a decimal', () => {
    expect(parseNumberText('12', int)).toEqual({ ok: true, value: 12 });
    expect(parseNumberText('0.7', unit)).toEqual({ ok: true, value: 0.7 });
    expect(parseNumberText('.5', unit)).toEqual({ ok: true, value: 0.5 });
  });

  it('keeps a reference as text', () => {
    expect(parseNumberText('${params.N}', int)).toEqual({
      ok: true,
      value: '${params.N}',
    });
  });

  it('refuses a text that is not a number or is out of range', () => {
    expect(parseNumberText('1.5', int)).toEqual({ ok: false });
    expect(parseNumberText('0', int)).toEqual({ ok: false });
    expect(parseNumberText('abc', unit)).toEqual({ ok: false });
    expect(parseNumberText('1.1', unit)).toEqual({ ok: false });
    expect(parseNumberText('-0.1', unit)).toEqual({ ok: false });
  });
});

describe('otherChatKeys', () => {
  it('lists the keys of `with` that no section edits', () => {
    expect(
      otherChatKeys({
        with: {
          agent: 'support',
          prompt: 'hi',
          model: 'm',
          tools: {},
          dags: [],
          max_tools: 3,
          stream: false,
          custom: 1,
          extra: { a: 1 },
        },
      })
    ).toEqual([
      ['agent', 'support'],
      ['custom', 1],
      ['extra', { a: 1 }],
    ]);
  });

  it('gives nothing when `with` is not a map', () => {
    expect(otherChatKeys({ with: 'x' })).toEqual([]);
    expect(otherChatKeys({})).toEqual([]);
  });
});

describe('chatIssueCounts', () => {
  it('assigns each issue to one section', () => {
    expect(
      chatIssueCounts({
        with: [issue('prompt or messages')],
        'with.provider': [issue('a')],
        'with.agent': [issue('a2')],
        'with.model[0].name': [issue('b')],
        'with.system': [issue('c')],
        'with.messages[1].role': [issue('d')],
        'with.temperature': [issue('e')],
        'with.dags[0]': [issue('f')],
        'with.max_tools': [issue('g')],
        'with.skills[0]': [issue('g2')],
        'with.unknown': [issue('h')],
        output_schema: [issue('i')],
      })
    ).toEqual({
      model: 2,
      system: 1,
      prompt: 2,
      options: 1,
      skills: 1,
      output: 1,
      other: 2,
    });
  });
});

describe('readMessages', () => {
  it('reads the rows and fills what is missing', () => {
    expect(readMessages([{ role: 'user', content: 'hi' }, 3])).toEqual([
      { role: 'user', content: 'hi' },
      { role: '', content: '' },
    ]);
    expect(readMessages('x')).toBeNull();
  });
});
