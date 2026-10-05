import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt, HiCheckCircle, HiXCircle } from 'react-icons/hi2';
import { authApi } from '../api';
import { useAuth } from '../contexts/AuthContext';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import './AuthViews.css';

type Status = 'verifying' | 'success' | 'error';

export default function VerifyEmailView() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { isAuthenticated, organization, refreshSession } = useAuth();
  const [status, setStatus] = useState<Status>('verifying');
  // Guards against the double-invocation of effects under React StrictMode, which
  // would otherwise consume the single-use token twice (second call → error).
  const startedRef = useRef(false);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;

    const token = searchParams.get('token');
    if (!token) {
      setStatus('error');
      return;
    }

    authApi
      .verifyEmail(token)
      .then(async () => {
        // Refresh the JWT so it carries email_verified=true and the dashboard gate
        // lifts immediately. We don't gate this on isAuthenticated: on a fresh page
        // load the auth state is still loading when this effect runs, so that flag
        // would be stale here. refreshSession() is a no-op without a stored refresh
        // token (e.g. verifying on a device where the user isn't logged in).
        try {
          await refreshSession();
        } catch {
          /* non-fatal: the dashboard gate also polls a refresh */
        }
        setStatus('success');
      })
      .catch(() => setStatus('error'));
  }, [searchParams, refreshSession]);

  const goToDashboard = () => {
    const slug = organization?.slug ?? 'default';
    navigate(`/organizations/${slug}`);
  };

  return (
    <div className="auth-container">
      <div className="auth-lang-switch">
        <LanguageSwitcher variant="compact" />
      </div>
      <div className="auth-card">
        <div className="auth-header">
          <div className="auth-logo brand-logo-icon brand-logo-icon-xl">
            <HiBolt />
          </div>
          <h1>{t('auth.verify.page_title')}</h1>
        </div>

        {status === 'verifying' && (
          <div className="verify-status">
            <div className="loading-spinner" />
            <p>{t('auth.verify.verifying')}</p>
          </div>
        )}

        {status === 'success' && (
          <div className="verify-status">
            <HiCheckCircle className="verify-icon verify-icon-success" />
            <h2>{t('auth.verify.success_title')}</h2>
            <p>{t('auth.verify.success_message')}</p>
            {isAuthenticated ? (
              <button className="auth-button" onClick={goToDashboard}>
                {t('auth.verify.go_to_dashboard')}
              </button>
            ) : (
              <Link className="auth-button" to="/login">
                {t('auth.verify.back_to_login')}
              </Link>
            )}
          </div>
        )}

        {status === 'error' && (
          <div className="verify-status">
            <HiXCircle className="verify-icon verify-icon-error" />
            <h2>{t('auth.verify.error_title')}</h2>
            <p>{t('auth.verify.error_message')}</p>
            <Link className="auth-button" to="/login">
              {t('auth.verify.back_to_login')}
            </Link>
          </div>
        )}
      </div>
    </div>
  );
}
