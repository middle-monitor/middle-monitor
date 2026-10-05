import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../i18n';
import { HiBolt, HiExclamationCircle, HiCheckCircle } from 'react-icons/hi2';
import { unsubscribeApi } from '../api';
import './AuthViews.css';

export default function UnsubscribeView() {
  const { t: tDefault } = useTranslation();
  const [params] = useSearchParams();
  // Recipients are not users: the app never detects their browser language, so
  // the outreach link carries it. Scoped with getFixedT rather than
  // changeLanguage, which would persist and flip a real customer's dashboard.
  const linkLang = params.get('lang');
  const lang = linkLang === 'fr' || linkLang === 'en' ? linkLang : null;
  const t = lang ? i18n.getFixedT(lang) : tDefault;
  const email = params.get('e') ?? '';
  const token = params.get('t') ?? '';
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  // The link language is not the app language: <html lang> follows it here, and
  // is restored on leave so the rest of the app keeps its own.
  useEffect(() => {
    document.title = t('unsubscribe.title');
    document.documentElement.lang = lang ?? i18n.language;
    return () => {
      document.documentElement.lang = i18n.language;
    };
    // Follows the link's language, not the app's: t is rebound on every language
    // change and would fight the restore in the cleanup.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lang]);

  // Confirmed by a click, never on load: mail scanners prefetch links and would
  // otherwise opt people out without them ever seeing the message.
  const handleConfirm = async () => {
    setError('');
    setIsLoading(true);
    try {
      await unsubscribeApi.confirm({ email, token });
      setDone(true);
    } catch {
      // The API answers 4xx in English and no Accept-Language is sent for a
      // recipient: showing data.error would break a deliberately French page.
      setError(t('unsubscribe.error_generic'));
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="auth-container">
      <div className="auth-card" style={{ maxWidth: '500px' }}>
        <div className="auth-header">
          <div className="auth-logo brand-logo-icon brand-logo-icon-xl">
            <HiBolt />
          </div>
          <h1>{t('unsubscribe.title')}</h1>
          <p>{done ? t('unsubscribe.done_subtitle') : t('unsubscribe.subtitle')}</p>
        </div>

        {error && (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{error}</span>
          </div>
        )}

        {done ? (
          <div className="verify-status">
            <HiCheckCircle className="verify-icon verify-icon-success" />
            <p>{t('unsubscribe.success', { email })}</p>
          </div>
        ) : !email || !token ? (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{t('unsubscribe.link_invalid')}</span>
          </div>
        ) : (
          <>
            <p style={{ textAlign: 'center', color: 'var(--text-secondary)' }}>{email}</p>
            <button
              type="button"
              className="auth-button"
              onClick={handleConfirm}
              disabled={isLoading}
              style={{ marginTop: '1rem' }}
            >
              {isLoading ? t('unsubscribe.confirming') : t('unsubscribe.confirm')}
            </button>
          </>
        )}

        <div className="auth-footer">
          <p>
            <Link to="/">{t('unsubscribe.back_home')}</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
