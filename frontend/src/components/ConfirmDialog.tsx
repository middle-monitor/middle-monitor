import { useEffect } from 'react';
import { HiOutlineExclamationTriangle, HiOutlineXMark } from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';

interface ConfirmDialogProps {
  title: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }: ConfirmDialogProps) {
  const { t } = useTranslation();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onCancel(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onCancel]);

  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal-content" style={{ maxWidth: 420 }} onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <span className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <HiOutlineExclamationTriangle style={{ color: 'var(--status-error)', fontSize: '1.25rem' }} />
            {title}
          </span>
          <button className="modal-close" onClick={onCancel}>
            <HiOutlineXMark />
          </button>
        </div>
        <div className="modal-body">
          <p style={{ color: 'var(--text-secondary)', margin: 0, lineHeight: 1.5 }}>{message}</p>
        </div>
        <div className="modal-footer">
          <button className="btn btn-secondary" onClick={onCancel}>{t('common.cancel')}</button>
          <button className="btn btn-danger" onClick={onConfirm}>{confirmLabel ?? t('common.delete')}</button>
        </div>
      </div>
    </div>
  );
}
