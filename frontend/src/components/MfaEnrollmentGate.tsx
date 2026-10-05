import { useTranslation } from 'react-i18next';
import { HiShieldCheck } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { TotpEnrollment } from './TotpEnrollment';
import './MfaEnrollmentGate.css';

/**
 * Blocks dashboard interaction when the org enforces 2FA but the user has not yet
 * enrolled an authenticator. The dashboard stays visible but blurred behind a modal
 * that walks the user through enrollment. Once it succeeds we refresh the session so
 * the new JWT carries totp_enabled=true and the gate lifts.
 */
export function MfaEnrollmentGate({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const { user, organization, logout, refreshSession } = useAuth();

  // Enrollment is required only when the org enforces 2FA and this user hasn't enrolled.
  const needsEnrollment = !!organization?.mfa_required && !!user && !user.totp_enabled;

  if (!needsEnrollment) return <>{children}</>;

  // Re-mint the token so totp_enabled flips to true and the gate lifts.
  const handleEnrolled = () => refreshSession().catch(() => {});

  return (
    <div className="mfa-gate">
      <div className="mfa-gate-content" aria-hidden="true">
        {children}
      </div>
      <div className="mfa-gate-overlay">
        <div className="mfa-gate-modal">
          <div className="mfa-gate-icon brand-logo-icon brand-logo-icon-lg">
            <HiShieldCheck />
          </div>
          <h2>{t('auth.mfa.gate_title')}</h2>
          <p className="mfa-gate-message">
            {t('auth.mfa.gate_subtitle', { org: organization?.name ?? '' })}
          </p>

          <TotpEnrollment onEnrolled={handleEnrolled} />

          <div className="mfa-gate-footer">
            <button className="mfa-gate-logout" onClick={logout}>
              {t('auth.mfa.logout')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
