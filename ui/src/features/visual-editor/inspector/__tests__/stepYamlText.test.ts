// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { applyEdit, parseDagDoc } from '../../doc';
import {
  modelArraySteps,
  yamlHandlers,
  yamlMapSteps,
  yamlUnindentedQuote,
} from '../../testing/fixtures';
import { parseStepYaml, stepYamlFromModel } from '../stepYamlText';
import { httpStep, jqStep, modelHttpJq } from './fixtures';

describe('stepYamlFromModel', () => {
  it('gives the step mapping at column 0', () => {
    expect(stepYamlFromModel(modelHttpJq, httpStep)).toBe(
      'id: fetch\nname: fetch\naction: http.request\nwith:\n  method: GET\n  url: ${params.API_URL}'
    );
    expect(stepYamlFromModel(modelHttpJq, jqStep).split('\n')[2]).toBe(
      'depends: [fetch]'
    );
  });

  it('keeps the lines of a block scalar', () => {
    const text = stepYamlFromModel(modelArraySteps, modelArraySteps.steps[1]!);
    expect(text.startsWith('name: transform')).toBe(true);
    expect(text).toContain(
      'run: |\n  head -n 1 data.csv\n  sort data.csv > sorted.csv'
    );
  });

  it('parses back to the step value', () => {
    for (const step of modelHttpJq.steps) {
      expect(parseStepYaml(stepYamlFromModel(modelHttpJq, step)).value).toEqual(
        step.value
      );
    }
  });

  // `replaceStepYaml` replaces the value of a map entry or a handler, so the
  // text leaves out its key.
  it.each([
    ['map entry', yamlMapSteps, 'wc -l data.csv', 'wc -c data.csv'],
    ['handler', yamlHandlers, 'echo ok', 'echo done'],
    ['handler with a comment', yamlHandlers, './notify.sh', './alert.sh'],
  ])('gives the value of a %s', (_, text, before, after) => {
    const model = parseDagDoc(text);
    const step = [...model.steps, ...model.handlers].find(
      (item) => item.runPreview === before
    )!;
    const yaml = stepYamlFromModel(model, step);
    expect(parseStepYaml(yaml).value).toEqual(step.value);
    expect(
      applyEdit(text, {
        kind: 'replaceStepYaml',
        step: step.key,
        yaml: yaml.replace(before, after),
      })
    ).toMatchObject({ ok: true, text: text.replace(before, after) });
  });
});

describe('parseStepYaml', () => {
  it('gives the position of a syntax error', () => {
    const check = parseStepYaml('name: a\nwith: [b\n');
    expect(check.ok).toBe(false);
    expect(check.error?.line).toBeGreaterThan(0);
    expect(check.error?.message).not.toBe('');
  });

  it.each(['- a\n- b', 'just text', ''])(
    'refuses %j that is not a mapping',
    (text) => {
      expect(parseStepYaml(text)).toEqual({
        ok: false,
        error: { message: 'The step must be a YAML mapping.' },
      });
    }
  );

  it('accepts a mapping', () => {
    expect(parseStepYaml('run: echo hi\n')).toEqual({
      ok: true,
      value: { run: 'echo hi' },
    });
  });
});

// The server reads a quoted scalar whatever the indent of its later lines:
// the inspector reads and replaces such a step too.
describe('step with a quoted scalar with unindented lines', () => {
  it('parses back to the step value and replaces it', () => {
    const model = parseDagDoc(yamlUnindentedQuote);
    const step = model.steps[0]!;
    const yaml = stepYamlFromModel(model, step);
    expect(parseStepYaml(yaml).value).toEqual(step.value);

    expect(
      applyEdit(yamlUnindentedQuote, {
        kind: 'replaceStepYaml',
        step: step.key,
        yaml: yaml.replace('-f1', '-f2'),
      })
    ).toMatchObject({
      ok: true,
      text: yamlUnindentedQuote.replace('-f1', '-f2'),
    });
  });
});
