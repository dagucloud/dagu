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

  it('keeps every Chinese catalog key aligned with English', () => {
    expect(Object.keys(messages['zh-CN']).sort()).toEqual(
      Object.keys(messages.en).sort()
    );
  });
});
