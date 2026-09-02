import { describe, expect, it } from 'vitest';
import { messages, translate } from '../messages';
import { staticMessages, translateStatic } from '../staticMessages';

describe('translations', () => {
  it('uses English as the default locale', () => {
    expect(translate('en', 'navigation.overview')).toBe('Overview');
  });

  it('provides Simplified Chinese translations for the initial UI shell', () => {
    expect(translate('zh-CN', 'navigation.overview')).toBe('概览');
    expect(translate('zh-CN', 'auth.signIn')).toBe('登录');
  });

  it('provides Japanese translations for the initial UI shell', () => {
    expect(translate('ja', 'navigation.overview')).toBe('概要');
    expect(translate('ja', 'auth.signIn')).toBe('ログイン');
  });

  it('translates the remaining application shell', () => {
    expect(translate('zh-CN', 'navigation.integrations')).toBe('集成');
    expect(translate('zh-CN', 'navigation.profilesSecrets')).toBe('配置与密钥');
    expect(translate('zh-CN', 'navigation.administration')).toBe('系统管理');
    expect(translate('zh-CN', 'theme.darkMode')).toBe('深色模式');
  });

  it('keeps every Chinese catalog key aligned with English', () => {
    expect(Object.keys(messages['zh-CN']).sort()).toEqual(
      Object.keys(messages.en).sort()
    );
  });

  it('keeps every Japanese catalog key aligned with English', () => {
    expect(Object.keys(messages.ja).sort()).toEqual(
      Object.keys(messages.en).sort()
    );
  });

  it('interpolates dynamic values', () => {
    expect(translate('ja', 'common.selected', { count: 3 })).toBe('3 件を選択');
  });

  it('keeps every static catalog aligned with English', () => {
    const englishKeys = Object.keys(staticMessages.en).sort();

    expect(Object.keys(staticMessages['zh-CN']).sort()).toEqual(englishKeys);
    expect(Object.keys(staticMessages.ja).sort()).toEqual(englishKeys);
  });

  it('translates first-party UI copy', () => {
    expect(translateStatic('zh-CN', 'No DAG runs found')).toBe(
      '未找到 DAG 运行'
    );
    expect(translateStatic('ja', 'No DAG runs found')).toBe(
      'DAG実行が見つかりません'
    );
  });

  it('interpolates complete static messages', () => {
    expect(
      translateStatic('zh-CN', 'No runs on {date}', { date: '2026-09-02' })
    ).toBe('2026-09-02 没有运行记录');
    expect(
      translateStatic(
        'ja',
        'Remove {count} missing items from sync tracking? Files remain in the remote repository.',
        { count: 2 }
      )
    ).toBe(
      '同期管理から欠落アイテムを 2 件削除しますか？ファイルはリモートリポジトリに残ります。'
    );
  });

  it('localizes plural messages without English suffixes', () => {
    expect(translateStatic('zh-CN', '{count} options', { count: 2 })).toBe(
      '2 个选项'
    );
    expect(translateStatic('ja', '{count} results', { count: 2 })).toBe(
      '2 件の結果'
    );
  });
});
