// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProps } from '../I18nProps';
import { I18nProvider } from '../I18nProvider';
import { I18nTemplate } from '../I18nTemplate';
import { I18nText } from '../I18nText';

function ButtonLabel({ buttonText }: { buttonText: string }) {
  return <span>{buttonText}</span>;
}

function EmptyMessage({ emptyMessage }: { emptyMessage: string }) {
  return <span>{emptyMessage}</span>;
}

describe('I18nProvider', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.lang = 'en';
  });

  it('sets the document language from the saved locale', async () => {
    localStorage.setItem(
      'user_preferences',
      JSON.stringify({ locale: 'zh-CN' })
    );

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    await waitFor(() => expect(document.documentElement.lang).toBe('zh-CN'));
  });

  it('sets Japanese as the document language', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    await waitFor(() => expect(document.documentElement.lang).toBe('ja'));
  });

  it('localizes static text and props', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <I18nProps>
            <input aria-label="Search" placeholder="Search items..." />
          </I18nProps>
          <span>
            <I18nText text="Actions" />
          </span>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByRole('textbox', { name: '検索' })).toHaveAttribute(
      'placeholder',
      '項目を検索...'
    );
    expect(screen.getByText('アクション')).toBeVisible();
  });

  it('localizes custom button text and interpolated text', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <I18nProps>
            <ButtonLabel buttonText="Delete" />
          </I18nProps>
          <I18nText text="No runs on {date}" values={{ date: '2026-09-02' }} />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByText('削除')).toBeVisible();
    expect(screen.getByText('2026-09-02 の実行はありません')).toBeVisible();
  });

  it('localizes rich templates and custom message props', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <I18nTemplate
            text="Rename {item} to a new path."
            values={{ item: <code>workflow.yaml</code> }}
          />
          <I18nProps>
            <EmptyMessage emptyMessage="No Wiki pages found" />
          </I18nProps>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByText('workflow.yaml')).toBeVisible();
    expect(screen.getByText('Wiki ページが見つかりません')).toBeVisible();
    expect(document.body).toHaveTextContent(
      'workflow.yamlのパスを変更します。'
    );
  });

  it('preserves rich template literals and unknown placeholders', () => {
    render(
      <>
        <I18nTemplate
          text="{item}item"
          values={{ item: <code>workflow</code> }}
        />{' '}
        <I18nTemplate text="{missing}" values={{}} />
      </>
    );

    expect(document.body).toHaveTextContent('workflowitem {missing}');
  });
});
