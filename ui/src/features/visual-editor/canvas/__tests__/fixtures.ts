// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  CanvasGraphInput,
  DagDocModel,
  StepKey,
  StepNameMap,
  StepView,
} from '../../contracts';

const DUMMY_RANGE = {
  from: 0,
  to: 0,
  startLine: 1,
  startColumn: 1,
  endLine: 1,
  endColumn: 1,
};

function indexOfKey(key: StepKey): number | null {
  const match = /^steps\[(\d+)\]$/.exec(key);
  return match ? Number(match[1]) : null;
}

/** Build a step view. The key is required; other fields get plain defaults. */
export function makeStep(
  partial: Partial<StepView> & { key: StepKey }
): StepView {
  return {
    index: indexOfKey(partial.key),
    form: 'block',
    kind: 'run',
    depends: [],
    value: {},
    range: DUMMY_RANGE,
    ...partial,
  };
}

/** Build a document model from steps. Steps and handlers keep the given order. */
export function makeModel(
  steps: StepView[],
  extra: Partial<DagDocModel> = {}
): DagDocModel {
  return {
    text: '',
    ok: true,
    parseErrors: [],
    documentCount: 1,
    dagType: 'graph',
    stepsForm: 'array',
    steps,
    handlers: [],
    ...extra,
  };
}

/** Server names of a model: the explicit name, else `step_<index>`. */
export function makeNames(model: DagDocModel): StepNameMap {
  const names: StepNameMap = { byKey: {}, byName: {} };
  model.steps.forEach((step, i) => {
    const name = step.explicitName ?? `step_${i + 1}`;
    names.byKey[step.key] = name;
    names.byName[name] = step.key;
  });
  return names;
}

const COMPONENTS = 6;

/**
 * Generated model of `stepCount` steps in six components. Inside a component
 * the steps form a binary tree, and each third step has a second parent. Some
 * steps are routers. Three handlers follow. The result is the same for each
 * call.
 */
export function makeLargeGraphInput(stepCount: number): CanvasGraphInput {
  const nameOf = (i: number) => `step${i}`;
  // Step `i` is number `Math.floor(i / 6)` of component `i % 6`.
  const at = (i: number, number: number) =>
    number * COMPONENTS + (i % COMPONENTS);
  const parents = (i: number): number[] => {
    const number = Math.floor(i / COMPONENTS);
    if (number === 0) {
      return [];
    }
    const list = [at(i, Math.floor((number - 1) / 2))];
    if (number % 3 === 0 && number > 1) {
      list.push(at(i, number - 1));
    }
    return [...new Set(list)];
  };
  const steps: StepView[] = [];
  for (let i = 0; i < stepCount; i++) {
    const depends = parents(i).map(nameOf);
    const router = i % 50 === 25;
    if (router) {
      const number = Math.floor(i / COMPONENTS);
      const targets = [2 * number + 1, 2 * number + 2, 2 * number + 3]
        .map((n) => at(i, n))
        .filter((t) => t < stepCount)
        .map(nameOf);
      const routes: Record<string, string[]> = {};
      targets.forEach((target, k) => {
        routes[`re:^case${k}$`] = [target];
      });
      steps.push(
        makeStep({
          key: `steps[${i}]`,
          explicitName: nameOf(i),
          kind: 'router',
          action: 'router.route',
          depends,
          value: { action: 'router.route', with: { value: '', routes } },
        })
      );
    } else {
      steps.push(
        makeStep({
          key: `steps[${i}]`,
          explicitName: nameOf(i),
          runPreview: `echo ${i}`,
          depends,
        })
      );
    }
  }
  const handlers = (['success', 'failure', 'exit'] as const).map((event) =>
    makeStep({
      key: `handler_on.${event}`,
      index: null,
      handler: event,
      runPreview: `echo ${event}`,
    })
  );
  const model = makeModel(steps, { handlers });
  return { model, names: makeNames(model), issues: { byStep: {}, dag: [] } };
}
