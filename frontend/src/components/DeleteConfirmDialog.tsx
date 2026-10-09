import { useEffect, useState } from 'react';
import { HiOutlineExclamationTriangle, HiOutlineXMark } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';

interface DeleteConfirmDialogProps {
  title: string;
  message: string;
  inputLabel: string;
  inputType: 'text' | 'password';
  // The typed value must equal this before the button unlocks; omit to accept any non-empty value.
  expected?: string;
  confirmLabel: string;
  onConfirm: (value: string) => Promise<void>;
  onCancel: () => void;
}

// Irreversible deletions ask for a typed value, so a stray click cannot erase anything.
export function DeleteConfirmDialog({
  title,
  message,
  inputLabel,
  inputType,
  expected,
  confirmLabel,
  onConfirm,
  onCancel,
}: DeleteConfirmDialogProps) {
  const { t } = useTranslation();
  const [value, setValue] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && !submitting) onCancel(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onCancel, submitting]);

  const ready = expected !== undefined ? value === expected : value !== '';

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready || submitting) return;
    setSubmitting(true);
    setError('');
    try {
      await onConfirm(value);
    } catch (err: any) {
      setError(err.response?.data?.error || t('common.error'));
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-overlay" onClick={submitting ? undefined : onCancel}>
      <form className="modal-content" style={{ maxWidth: 460 }} onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <div className="modal-header">
          <span className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <HiOutlineExclamationTriangle style={{ color: 'var(--status-error)', fontSize: '1.25rem' }} />
            {title}
          </span>
          <button type="button" className="modal-close" onClick={onCancel} disabled={submitting}>
            <HiOutlineXMark />
          </button>
        </div>
        <div className="modal-body">
          <p style={{ color: 'var(--text-secondary)', margin: '0 0 1rem', lineHeight: 1.5 }}>{message}</p>
          <label className="label" htmlFor="delete-confirm-input">{inputLabel}</label>
          <input
            id="delete-confirm-input"
            className="input"
            type={inputType}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoComplete={inputType === 'password' ? 'current-password' : 'off'}
            autoFocus
          />
          {error && <div className="error-message" style={{ marginTop: '0.75rem' }}>{error}</div>}
        </div>
        <div className="modal-footer">
          <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={submitting}>
            {t('common.cancel')}
          </button>
          <button type="submit" className="btn btn-danger" disabled={!ready || submitting}>
            {confirmLabel}
          </button>
        </div>
      </form>
    </div>
  );
}
