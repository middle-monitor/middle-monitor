import { useTranslation } from 'react-i18next';
import { HiLanguage } from 'react-icons/hi2';
import './LanguageSwitcher.css';

interface LanguageSwitcherProps {
  variant?: 'default' | 'compact' | 'pill';
  className?: string;
}

const SUPPORTED = ['fr', 'en'] as const;
type Lang = (typeof SUPPORTED)[number];

const LANG_LABELS: Record<Lang, { full: string; short: string }> = {
  fr: { full: 'Français', short: 'FR' },
  en: { full: 'English', short: 'EN' },
};

export function LanguageSwitcher({ variant = 'default', className = '' }: LanguageSwitcherProps) {
  const { i18n, t } = useTranslation();

  const currentLang: Lang = i18n.language.startsWith('fr') ? 'fr' : 'en';
  const nextLang: Lang = currentLang === 'fr' ? 'en' : 'fr';

  const switchTo = (lang: Lang) => {
    if (lang === currentLang) return;
    void i18n.changeLanguage(lang);
  };

  // Pill variant: shows both languages side by side, the active one highlighted.
  // Used on landing/marketing pages where the language choice should be obvious.
  if (variant === 'pill') {
    return (
      <div
        className={`language-switcher-pill ${className}`}
        role="group"
        aria-label={t('sidebar.language')}
      >
        {SUPPORTED.map((lang) => (
          <button
            key={lang}
            type="button"
            className={`language-switcher-pill-option ${currentLang === lang ? 'is-active' : ''}`}
            onClick={() => switchTo(lang)}
            aria-pressed={currentLang === lang}
            title={LANG_LABELS[lang].full}
          >
            {LANG_LABELS[lang].short}
          </button>
        ))}
      </div>
    );
  }

  // Compact / default: single button toggling to the other language. Title and
  // aria-label are localized so screen readers report the right action.
  const currentLabel = LANG_LABELS[currentLang].full;
  const nextLabel = LANG_LABELS[nextLang].full;

  return (
    <button
      type="button"
      className={`language-switcher language-switcher--${variant} ${className}`}
      onClick={() => switchTo(nextLang)}
      title={`${currentLabel} → ${nextLabel}`}
      aria-label={`${t('sidebar.language')}: ${currentLabel}. Switch to ${nextLabel}.`}
    >
      <HiLanguage className="language-switcher-icon" aria-hidden="true" />
      {variant !== 'compact' && <span className="language-switcher-label">{currentLabel}</span>}
      {variant === 'compact' && (
        <span className="language-switcher-code" aria-hidden="true">
          {LANG_LABELS[currentLang].short}
        </span>
      )}
    </button>
  );
}
