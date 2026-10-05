import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt, HiExclamationCircle } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import './AuthViews.css';

export default function LoginView() {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  // When the org enforces 2FA, the password step returns a challenge token and we
  // switch to the code step instead of navigating straight to the dashboard.
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [code, setCode] = useState('');
  const { login, completeMfaLogin } = useAuth();
  const navigate = useNavigate();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsLoading(true);

    try {
      const result = await login(email, password);
      if (result?.status === 'mfa_required') {
        setMfaToken(result.mfaToken);
        return;
      }
      const slug = result?.organization?.slug ?? 'default';
      navigate(`/organizations/${slug}`);
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.login.error_generic'));
    } finally {
      setIsLoading(false);
    }
  };

  const handleMfaSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!mfaToken) return;
    setError('');
    setIsLoading(true);

    try {
      const result = await completeMfaLogin(mfaToken, code.trim());
      const slug = result?.organization?.slug ?? 'default';
      navigate(`/organizations/${slug}`);
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.login.error_generic'));
    } finally {
      setIsLoading(false);
    }
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
          <h1>{mfaToken ? t('auth.login.mfa_title') : t('auth.login.title')}</h1>
          <p>{mfaToken ? t('auth.login.mfa_subtitle') : t('auth.login.subtitle')}</p>
        </div>

        {error && (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{error}</span>
          </div>
        )}

        {mfaToken ? (
          <form onSubmit={handleMfaSubmit} className="auth-form">
            <div className="form-group">
              <label htmlFor="mfa-code">{t('auth.login.mfa_code')}</label>
              <input
                id="mfa-code"
                type="text"
                inputMode="numeric"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder={t('auth.login.mfa_code_placeholder')}
                required
                autoFocus
                autoComplete="one-time-code"
              />
            </div>

            <p className="auth-hint">{t('auth.login.mfa_hint')}</p>

            <button type="submit" className="auth-button" disabled={isLoading}>
              {isLoading ? t('auth.login.submitting') : t('auth.login.mfa_submit')}
            </button>
          </form>
        ) : (
          <form onSubmit={handleSubmit} className="auth-form">
            <div className="form-group">
              <label htmlFor="email">{t('auth.login.email')}</label>
              <input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={t('auth.login.email_placeholder')}
                required
                autoComplete="email"
              />
            </div>

            <div className="form-group">
              <label htmlFor="password">{t('auth.login.password')}</label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                required
                autoComplete="current-password"
              />
            </div>

            <div className="auth-form-aside">
              <Link to="/reset-password">{t('auth.login.forgot_password')}</Link>
            </div>

            <button type="submit" className="auth-button" disabled={isLoading}>
              {isLoading ? t('auth.login.submitting') : t('auth.login.submit')}
            </button>
          </form>
        )}

        <div className="auth-footer">
          <p>
            {t('auth.login.no_account')}{' '}
            <Link to="/get-started">{t('auth.login.create_one')}</Link>
          </p>
          <p>
            <Link to="/">{t('auth.login.back_to_home')}</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
