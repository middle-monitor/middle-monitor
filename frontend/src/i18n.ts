import i18n from 'i18next';

import { initReactI18next } from 'react-i18next';

import enTranslation from './locales/en.json';
import frTranslation from './locales/fr.json';

const SUPPORTED = ['en', 'fr'] as const;
type Lang = (typeof SUPPORTED)[number];

function detectInitialLanguage(): Lang {
  const stored =
    (typeof localStorage !== 'undefined' && localStorage.getItem('appLang')) ||
    '';
  if (stored && SUPPORTED.includes(stored as Lang)) return stored as Lang;

  return 'en';
}

const initialLanguage = detectInitialLanguage();

i18n.use(initReactI18next).init({
  resources: {
    en: { translation: enTranslation },
    fr: { translation: frTranslation },
  },
  lng: initialLanguage,
  fallbackLng: 'fr',
  supportedLngs: SUPPORTED as unknown as string[],
  interpolation: {
    escapeValue: false,
  },
  react: {
    useSuspense: false,
  },
});

// Keep <html lang> and document.dir in sync with the active language so that
// browser features (spellcheck, readers, hyphenation) and CSS `:lang(en)`
// selectors work correctly. Done both at boot and on every change.
function applyLangAttributes(lang: string) {
  if (typeof document === 'undefined') return;
  document.documentElement.lang = lang;
  document.documentElement.dir = 'ltr';
}

applyLangAttributes(initialLanguage);
i18n.on('languageChanged', (lng) => {
  applyLangAttributes(lng);
  try {
    localStorage.setItem('appLang', lng);
  } catch {
    /* ignore quota / private mode */
  }
});

export default i18n;
