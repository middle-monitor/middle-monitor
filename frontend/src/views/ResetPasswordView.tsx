import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt, HiExclamationCircle, HiCheckCircle } from 'react-icons/hi2';
import { authApi } from '../api';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import './AuthViews.css';

// Mirrors the backend password policy so the user gets immediate feedback.
const PASSWORD_RE = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/;

// Single view for the whole forgotten-password flow. Without a `token` query
// param it shows the "request a reset link" form; with one it shows the
// "choose a new password" form.
export default function ResetPasswordView() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token') || '';

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [done, setDone] = useState(false);

  const handleRequest = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsLoading(true);
    try {
      await authApi.forgotPassword(email.trim());
      setDone(true);
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.forgot.request_error'));
    } finally {
      setIsLoading(false);
    }
  };

  const handleReset = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!PASSWORD_RE.test(password)) {
      setError(t('auth.forgot.error_password_weak'));
      return;
    }
    if (password !== confirm) {
      setError(t('auth.forgot.error_password_mismatch'));
      return;
    }
    setError('');
    setIsLoading(true);
    try {
      await authApi.resetPassword(token, password);
      setDone(true);
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.forgot.error_generic'));
    } finally {
      setIsLoading(false);
    }
  };

  const renderBody = () => {
    // Reset mode: link clicked, choose a new password.
    if (token) {
      if (done) {
        return (
          <div className="verify-status">
            <HiCheckCircle className="verify-icon verify-icon-success" />
            <h2>{t('auth.forgot.success_title')}</h2>
            <p>{t('auth.forgot.success_message')}</p>
            <Link to="/login" className="auth-button">
              {t('auth.forgot.go_to_login')}
            </Link>
          </div>
        );
      }
      return (
        <form onSubmit={handleReset} className="auth-form">
          <div className="form-group">
            <label htmlFor="password">{t('auth.forgot.password')}</label>
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
            <label htmlFor="confirm">{t('auth.forgot.confirm_password')}</label>
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
            {isLoading ? t('auth.forgot.reset_submitting') : t('auth.forgot.reset_submit')}
          </button>
        </form>
      );
    }

    // Request mode: ask for the email to send the reset link.
    if (done) {
      return (
        <div className="verify-status">
          <HiCheckCircle className="verify-icon verify-icon-success" />
          <h2>{t('auth.forgot.request_sent_title')}</h2>
          <p>{t('auth.forgot.request_sent_message')}</p>
        </div>
      );
    }
    return (
      <form onSubmit={handleRequest} className="auth-form">
        <div className="form-group">
          <label htmlFor="email">{t('auth.forgot.email')}</label>
          <input
            id="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={t('auth.forgot.email_placeholder')}
            required
            autoComplete="email"
            autoFocus
          />
        </div>

        <button type="submit" className="auth-button" disabled={isLoading}>
          {isLoading ? t('auth.forgot.request_submitting') : t('auth.forgot.request_submit')}
        </button>
      </form>
    );
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
          <h1>{token ? t('auth.forgot.reset_title') : t('auth.forgot.request_title')}</h1>
          <p>{token ? t('auth.forgot.reset_subtitle') : t('auth.forgot.request_subtitle')}</p>
        </div>

        {error && (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{error}</span>
          </div>
        )}

        {renderBody()}

        <div className="auth-footer">
          <p>
            <Link to="/login">{t('auth.forgot.back_to_login')}</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
