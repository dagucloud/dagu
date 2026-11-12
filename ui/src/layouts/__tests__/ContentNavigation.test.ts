// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { translate } from '@/i18n/messages';
import { getBreadcrumbItems } from '../ContentNavigation';

const last = (list: string[]) => list[list.length - 1];

describe('getBreadcrumbItems', () => {
  // The last segment of a workflow page comes from the catalog, so the
  // Executions page of a workflow reads in the language of the user.
  it('translates the Executions segment of a workflow page', () => {
    const labels = (locale: 'en' | 'zh-CN' | 'ja') =>
      getBreadcrumbItems('/dags/etl/executions', (key) =>
        translate(locale, key)
      ).map((item) => item.label);

    expect(labels('en')).toEqual(['Home', 'Workflows', 'etl', 'Executions']);
    expect(last(labels('ja'))).toBe('実行履歴');
    expect(last(labels('zh-CN'))).toBe('执行记录');
  });

  // An old link to the History tab keeps its crumb name until it redirects.
  it('keeps the History crumb of the old tab link', () => {
    const items = getBreadcrumbItems('/dags/etl/history');
    expect(last(items.map((item) => item.label))).toBe('History');
  });

  // A calendar is named in schedules by its file name, so the crumb keeps
  // it as written.
  it('shows a calendar by its name', () => {
    expect(getBreadcrumbItems('/calendars/jp-banking')).toEqual([
      { label: 'Home', to: '/home' },
      { label: 'Calendars', to: '/calendars' },
      { label: 'jp-banking', to: '/calendars/jp-banking' },
    ]);
  });

  it('keeps the remote node of a calendar page in its crumbs', () => {
    expect(
      getBreadcrumbItems('/calendars/jp-banking', undefined, '?remoteNode=edge')
        .slice(1)
        .map((item) => item.to)
    ).toEqual([
      '/calendars?remoteNode=edge',
      '/calendars/jp-banking?remoteNode=edge',
    ]);
  });

  it('keeps an unknown segment as its path name', () => {
    const items = getBreadcrumbItems('/dags/etl/some-page');
    expect(last(items.map((item) => item.label))).toBe('Some Page');
  });
});
