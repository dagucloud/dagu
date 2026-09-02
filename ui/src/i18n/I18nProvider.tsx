import { createContext, useContext, useEffect, useMemo } from 'react';
import { useUserPreferences, type Locale } from '@/contexts/UserPreference';
import {
  translate,
  type TranslationKey,
  type TranslationValues,
} from './messages';

type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: TranslationKey, values?: TranslationValues) => string;
};

const defaultI18nContext: I18nContextValue = {
  locale: 'en',
  setLocale: () => {},
  t: (key, values) => translate('en', key, values),
};

const I18nContext = createContext<I18nContextValue>(defaultI18nContext);

export function I18nProvider({ children }: { children: React.ReactNode }) {
  const { preferences, updatePreference } = useUserPreferences();

  useEffect(() => {
    document.documentElement.lang = preferences.locale;
  }, [preferences.locale]);

  const value = useMemo<I18nContextValue>(
    () => ({
      locale: preferences.locale,
      setLocale: (locale) => updatePreference('locale', locale),
      t: (key, values) => translate(preferences.locale, key, values),
    }),
    [preferences.locale, updatePreference]
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}
