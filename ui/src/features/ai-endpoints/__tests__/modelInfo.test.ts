// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { modelInfos, offeredEfforts } from '../modelInfo';

describe('modelInfos', () => {
  it('gives the bare name of a model that modelInfo does not describe', () => {
    expect(
      modelInfos({ models: ['a', 'b'], modelInfo: [{ id: 'b' }] })
    ).toEqual([{ id: 'a' }, { id: 'b' }]);
    expect(modelInfos(undefined)).toEqual([]);
    expect(modelInfos({ models: [], modelInfo: [] })).toEqual([]);
  });

  it('follows the order of models and reads the infos by id', () => {
    const choice = {
      models: ['a', 'b'],
      modelInfo: [
        { id: 'b', displayName: 'Model B', efforts: ['low', 'max'] },
        { id: 'a', efforts: [] },
      ],
    };
    expect(modelInfos(choice)).toEqual([
      { id: 'a', efforts: [] },
      { id: 'b', displayName: 'Model B', efforts: ['low', 'max'] },
    ]);
  });

  // The models list is the source: an info of a name that it lacks is not a model.
  it('ignores an info whose model is not in the list', () => {
    expect(
      modelInfos({ models: ['a'], modelInfo: [{ id: 'gone' }, { id: 'a' }] })
    ).toEqual([{ id: 'a' }]);
  });
});

describe('offeredEfforts', () => {
  it('offers the levels of the model, in the order of the ladder', () => {
    expect(
      offeredEfforts({ id: 'a', efforts: ['max', 'low', 'none', 'high'] }, true)
    ).toEqual(['low', 'high', 'max']);
  });

  it('offers the whole ladder or nothing when the model has no info', () => {
    expect(offeredEfforts({ id: 'a' }, true)).toEqual([
      'low',
      'medium',
      'high',
      'xhigh',
      'max',
    ]);
    expect(offeredEfforts(undefined, false)).toEqual([]);
    expect(offeredEfforts({ id: 'a', efforts: [] }, true)).toEqual([]);
  });
});
