import { useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt, HiExclamationCircle } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import './AuthViews.css';

// Mirrors the backend password policy so the user gets immediate feedback.
const PASSWORD_RE = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/;

export default function AcceptInviteView() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { acceptInvite } = useAuth();
  const token = searchParams.get('token') || '';

  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!PASSWORD_RE.test(password)) {
      setError(t('auth.accept.error_password_weak'));
      return;
    }
    if (password !== confirm) {
      setError(t('auth.accept.error_password_mismatch'));
      return;
    }
    setError('');
    setIsLoading(true);
    try {
      const result = await acceptInvite(token, password);
      const slug = result?.organization?.slug ?? 'default';
      navigate(`/organizations/${slug}`);
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.accept.error_generic'));
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
          <h1>{t('auth.accept.page_title')}</h1>
          <p>{t('auth.accept.subtitle')}</p>
        </div>

        {error && (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{error}</span>
          </div>
        )}

        {!token ? (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{t('auth.accept.missing_token')}</span>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="auth-form">
            <div className="form-group">
              <label htmlFor="password">{t('auth.accept.password')}</label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                required
                autoComplete="new-password"
              />
            </div>

            <div className="form-group">
              <label htmlFor="confirm">{t('auth.accept.confirm_password')}</label>
              <input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder="••••••••"
                required
                autoComplete="new-password"
              />
            </div>

            <button type="submit" className="auth-button" disabled={isLoading}>
              {isLoading ? t('auth.accept.submitting') : t('auth.accept.submit')}
            </button>
          </form>
        )}

        <div className="auth-footer">
          <p>
            <Link to="/login">{t('auth.accept.back_to_login')}</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
