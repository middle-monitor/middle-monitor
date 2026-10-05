import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiOutlineEnvelope } from 'react-icons/hi2';
import { authApi } from '../api';
import { useAuth } from '../contexts/AuthContext';
import './EmailVerificationGate.css';

type ResendState = 'idle' | 'sending' | 'sent' | 'error';

/**
 * Blocks dashboard interaction until the user confirms their email. The dashboard
 * stays visible but blurred behind a modal. While locked, it polls a token refresh
 * every few seconds: when the email gets verified (here or on another device), the
 * refreshed JWT carries email_verified=true, the backend gate lifts, and this
 * component reveals the dashboard automatically.
 */
export function EmailVerificationGate({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const { user, logout, refreshSession } = useAuth();
  const [resendState, setResendState] = useState<ResendState>('idle');

  // Treat an absent flag (e.g. legacy tokens) as verified so we never lock out
  // accounts that predate the verification feature.
  const verified = user?.email_verified !== false;

  useEffect(() => {
    if (verified) return;
    const id = window.setInterval(() => {
      refreshSession().catch(() => {
        /* refresh token may be expired; the user can re-login */
      });
    }, 5000);
    return () => window.clearInterval(id);
  }, [verified, refreshSession]);

  if (verified) return <>{children}</>;

  const handleResend = async () => {
    setResendState('sending');
    try {
      await authApi.resendVerification();
      setResendState('sent');
    } catch {
      setResendState('error');
    }
  };

  return (
    <div className="email-gate">
      <div className="email-gate-content" aria-hidden="true">
        {children}
      </div>
      <div className="email-gate-overlay">
        <div className="email-gate-modal">
          <div className="email-gate-icon brand-logo-icon brand-logo-icon-lg">
            <HiOutlineEnvelope />
          </div>
          <h2>{t('auth.verify.gate_title')}</h2>
          <p className="email-gate-message">
            {t('auth.verify.gate_message', { email: user?.email ?? '' })}
          </p>
          <p className="email-gate-hint">{t('auth.verify.gate_hint')}</p>

          {resendState === 'sent' && (
            <p className="email-gate-feedback email-gate-feedback-ok">
              {t('auth.verify.resent')}
            </p>
          )}
          {resendState === 'error' && (
            <p className="email-gate-feedback email-gate-feedback-error">
              {t('auth.verify.resend_error')}
            </p>
          )}

          <button
            className="auth-button"
            onClick={handleResend}
            disabled={resendState === 'sending'}
          >
            {resendState === 'sending'
              ? t('auth.verify.resending')
              : t('auth.verify.resend')}
          </button>

          <div className="email-gate-footer">
            <span className="email-gate-checking">{t('auth.verify.checking')}</span>
            <button className="email-gate-logout" onClick={logout}>
              {t('auth.verify.logout')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
