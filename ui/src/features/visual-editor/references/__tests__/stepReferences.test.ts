// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { referencedStepNames } from '../stepReferences';

const steps = [
  { name: 'extract orders', id: 'extract_orders', output: 'ORDERS' },
  { name: 'extract users', id: 'extract_users' },
  { name: 'plain' },
  { name: 'merge', id: 'merge' },
];

describe('referencedStepNames', () => {
  it('finds a direct reference by the id of a step', () => {
    expect(
      referencedStepNames(
        steps,
        'merge',
        'echo ${steps.extract_orders.outputs.orders} ${steps.extract_users.outputs.users}'
      )
    ).toEqual(['extract orders', 'extract users']);
  });

  it('finds a reference by the name of a step that has no id', () => {
    expect(
      referencedStepNames(steps, 'merge', 'echo ${steps.plain.outputs.x}')
    ).toEqual(['plain']);
  });

  it('finds a reference through an alias, the output variable of a step', () => {
    expect(referencedStepNames(steps, 'merge', 'echo ${ORDERS}')).toEqual([
      'extract orders',
    ]);
    expect(referencedStepNames(steps, 'merge', 'echo ${ORDERS.count}')).toEqual(
      ['extract orders']
    );
  });

  it('finds the older dotted forms', () => {
    expect(
      referencedStepNames(
        steps,
        'merge',
        '${extract_users.stdout} ${extract_orders.outputs.orders}'
      )
    ).toEqual(['extract orders', 'extract users']);
  });

  it('lists a step once, in the order of the workflow', () => {
    expect(
      referencedStepNames(
        steps,
        'merge',
        '${steps.extract_users.outputs.u} ${ORDERS} ${steps.extract_users.outputs.u}'
      )
    ).toEqual(['extract orders', 'extract users']);
  });

  it('finds nothing in a text with no reference', () => {
    expect(referencedStepNames(steps, 'merge', 'echo hello')).toEqual([]);
    expect(referencedStepNames(steps, 'merge', '')).toEqual([]);
    expect(
      referencedStepNames(steps, 'merge', 'echo ${params.TARGET} ${HOME}')
    ).toEqual([]);
  });

  it('ignores a step that the workflow does not have', () => {
    expect(
      referencedStepNames(steps, 'merge', 'echo ${steps.ghost.outputs.x}')
    ).toEqual([]);
    expect(referencedStepNames(steps, 'merge', 'echo ${GHOST}')).toEqual([]);
  });

  it('ignores an escaped reference and a reference to the step itself', () => {
    expect(
      referencedStepNames(
        steps,
        'merge',
        'echo \\${steps.plain.outputs.x} ${steps.merge.outputs.m}'
      )
    ).toEqual([]);
  });

  it('gives nothing without steps', () => {
    expect(referencedStepNames(undefined, 'merge', '${ORDERS}')).toEqual([]);
  });
});
