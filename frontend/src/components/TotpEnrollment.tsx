import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiClipboard, HiExclamationTriangle } from 'react-icons/hi2';
import { authApi } from '../api';
import './TotpEnrollment.css';

type Step = 'loading' | 'scan' | 'recovery' | 'error';

interface TotpEnrollmentProps {
  /** Called once enrollment is fully complete (after the user acknowledges the
   *  recovery codes). The parent typically refreshes the session/user here. */
  onEnrolled: () => void;
  /** Optional cancel action. Shown only when provided (e.g. account settings); the
   *  forced enrollment gate omits it. */
  onCancel?: () => void;
}

/**
 * Self-contained TOTP enrollment flow: fetch a secret + QR, confirm a code, then
 * reveal the one-time recovery codes. Used both by the forced MfaEnrollmentGate and
 * by the voluntary opt-in in account settings.
 */
export function TotpEnrollment({ onEnrolled, onCancel }: TotpEnrollmentProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>('loading');
  const [qrPng, setQrPng] = useState('');
  const [secret, setSecret] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [copied, setCopied] = useState(false);

  const startSetup = useCallback(async () => {
    setStep('loading');
    setError('');
    try {
      const res = await authApi.setup2fa();
      setQrPng(res.data.qr_png);
      setSecret(res.data.secret);
      setStep('scan');
    } catch {
      setStep('error');
    }
  }, []);

  // Run setup exactly once. Without this guard, React StrictMode (dev) invokes the
  // effect twice, firing two /2fa/setup calls that each generate and persist a new
  // secret. The QR shown (last response to resolve) could then differ from the
  // stored secret (last UPDATE to commit), making the scanned code fail at random —
  // and persisting the wrong secret, which also breaks the later login 2FA.
  const didSetup = useRef(false);
  useEffect(() => {
    if (didSetup.current) return;
    didSetup.current = true;
    startSetup();
  }, [startSetup]);

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsSubmitting(true);
    try {
      const res = await authApi.verify2fa(code.trim());
      setRecoveryCodes(res.data.recovery_codes);
      setStep('recovery');
    } catch (err: any) {
      setError(err.response?.data?.error || t('auth.mfa.error_invalid_code'));
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleCopyCodes = async () => {
    try {
      await navigator.clipboard.writeText(recoveryCodes.join('\n'));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard may be unavailable; the codes are visible on screen anyway */
    }
  };

  if (step === 'loading') {
    return <p className="totp-message">{t('common.loading')}</p>;
  }

  if (step === 'error') {
    return (
      <>
        <p className="totp-feedback-error">{t('auth.mfa.error_setup')}</p>
        <button type="button" className="auth-button" onClick={startSetup}>
          {t('auth.mfa.retry')}
        </button>
      </>
    );
  }

  if (step === 'recovery') {
    return (
      <>
        <h3 className="totp-title">{t('auth.mfa.recovery_title')}</h3>
        <p className="totp-message">{t('auth.mfa.recovery_intro')}</p>
        <ul className="totp-recovery-codes">
          {recoveryCodes.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
        <div className="totp-warning">
          <HiExclamationTriangle />
          <span>{t('auth.mfa.recovery_warning')}</span>
        </div>
        <button type="button" className="btn-secondary totp-copy-btn" onClick={handleCopyCodes}>
          <HiClipboard /> {copied ? t('auth.mfa.copied') : t('auth.mfa.copy_codes')}
        </button>
        <button type="button" className="auth-button" onClick={onEnrolled}>
          {t('auth.mfa.continue_button')}
        </button>
      </>
    );
  }

  // step === 'scan'
  return (
    <>
      <p className="totp-hint">{t('auth.mfa.scan_instructions')}</p>
      {qrPng && (
        <img className="totp-qr" src={`data:image/png;base64,${qrPng}`} alt={t('auth.mfa.qr_alt')} />
      )}
      <p className="totp-hint">{t('auth.mfa.manual_entry')}</p>
      <code className="totp-secret">{secret}</code>

      <form onSubmit={handleVerify} className="totp-form">
        <input
          type="text"
          inputMode="numeric"
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
          placeholder={t('auth.mfa.code_placeholder')}
          required
          autoFocus
          maxLength={6}
          autoComplete="one-time-code"
        />
        {error && <p className="totp-feedback-error">{error}</p>}
        <button type="submit" className="auth-button" disabled={isSubmitting}>
          {isSubmitting ? t('auth.mfa.verifying') : t('auth.mfa.verify_button')}
        </button>
        {onCancel && (
          <button type="button" className="totp-cancel" onClick={onCancel}>
            {t('common.cancel')}
          </button>
        )}
      </form>
    </>
  );
}
