import { describe, expect, it } from 'vitest';
import { messages, translate } from '../messages';

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
});
