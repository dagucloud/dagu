// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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

// The setup file loads every catalog. Fresh modules start without them, as
// the app does on its first load.
async function importFreshI18n() {
  vi.resetModules();
  const [preferences, provider, text, catalogs] = await Promise.all([
    import('@/contexts/UserPreference'),
    import('../I18nProvider'),
    import('../I18nText'),
    import('../translateStatic'),
  ]);
  return {
    UserPreferencesProvider: preferences.UserPreferencesProvider,
    I18nProvider: provider.I18nProvider,
    useI18n: provider.useI18n,
    I18nText: text.I18nText,
    loadStaticMessages: catalogs.loadStaticMessages,
  };
}

describe('I18nProvider', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.lang = 'en';
  });

  afterEach(() => {
    vi.doUnmock('../staticMessages');
    vi.restoreAllMocks();
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

  it('holds the first render until the saved locale loads', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    const { UserPreferencesProvider, I18nProvider, I18nText, ...i18n } =
      await importFreshI18n();

    await act(async () => {
      render(
        <UserPreferencesProvider>
          <I18nProvider>
            <span>
              <I18nText text="Actions" />
            </span>
          </I18nProvider>
        </UserPreferencesProvider>
      );
    });
    expect(screen.queryByText('Actions')).not.toBeInTheDocument();

    await act(() => i18n.loadStaticMessages('ja'));
    expect(screen.getByText('アクション')).toBeVisible();
  });

  it('switches to a locale whose catalog has not loaded', async () => {
    const { UserPreferencesProvider, I18nProvider, I18nText, ...i18n } =
      await importFreshI18n();
    function SwitchToJapanese() {
      const { setLocale } = i18n.useI18n();
      return <button onClick={() => setLocale('ja')}>Switch</button>;
    }

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <SwitchToJapanese />
          <span>
            <I18nText text="Actions" />
          </span>
        </I18nProvider>
      </UserPreferencesProvider>
    );
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Switch' }));
    });

    expect(screen.getByText('アクション')).toBeVisible();
  });

  // A tab left open across a server upgrade asks for a chunk that no longer
  // exists; the app must stay usable.
  it('shows English when the catalog fails to load', async () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    vi.doMock('../staticMessages', () => {
      throw new Error('chunk not found');
    });
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { UserPreferencesProvider, I18nProvider, I18nText, ...i18n } =
      await importFreshI18n();

    await act(async () => {
      render(
        <UserPreferencesProvider>
          <I18nProvider>
            <span>
              <I18nText text="Actions" />
            </span>
          </I18nProvider>
        </UserPreferencesProvider>
      );
    });
    await act(() => i18n.loadStaticMessages('ja'));

    expect(screen.getByText('Actions')).toBeVisible();
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
