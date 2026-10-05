import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiOutlineWrenchScrewdriver, HiOutlineXMark } from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';

interface Props {
  targetType: 'service' | 'host';
  targetId: number;
  targetName: string;
  onClose: () => void;
  onCreated: () => void;
}

function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function ScheduleDowntimeModal({ targetType, targetId, targetName, onClose, onCreated }: Props) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();

  const now = new Date();
  const [form, setForm] = useState({
    name: '',
    starts_at: toLocalInput(now),
    ends_at: toLocalInput(new Date(now.getTime() + 2 * 60 * 60 * 1000)),
  });
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    if (!form.name.trim()) {
      setError(t('maintenance.error_required'));
      return;
    }
    const start = new Date(form.starts_at);
    const end = new Date(form.ends_at);
    if (!(end > start)) {
      setError(t('maintenance.error_range'));
      return;
    }
    setSubmitting(true);
    try {
      await orgApi.maintenance.create({
        name: form.name.trim(),
        target_type: targetType,
        target_id: targetId,
        starts_at: start.toISOString(),
        ends_at: end.toISOString(),
      });
      onCreated();
      onClose();
    } catch (err: any) {
      setError(err.response?.data?.error || t('maintenance.error_create'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="alert-modal-overlay" onClick={onClose}>
      <div className="alert-modal" style={{ maxWidth: 480 }} onClick={(e) => e.stopPropagation()}>
        <div className="alert-modal-header">
          <h3 style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <HiOutlineWrenchScrewdriver />
            {t('maintenance.schedule')}
          </h3>
          <button className="btn-icon" onClick={onClose}><HiOutlineXMark /></button>
        </div>
        <form onSubmit={handleSubmit} className="alert-form">
          <div className="form-row">
            <label>{t('maintenance.field_target')}</label>
            <div style={{ padding: '0.5rem 0.75rem', background: 'var(--bg-secondary)', border: '1px solid var(--border-primary)', borderRadius: 6, fontSize: '0.875rem', color: 'var(--text-secondary)' }}>
              {t(`maintenance.target_${targetType}`)}: <strong style={{ color: 'var(--text-primary)' }}>{targetName}</strong>
            </div>
          </div>
          <div className="form-row">
            <label>{t('maintenance.field_name')}</label>
            <input
              className="form-control"
              value={form.name}
              placeholder={t('maintenance.name_placeholder')}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>
          <div className="form-row-group">
            <div className="form-row">
              <label>{t('maintenance.field_start')}</label>
              <input type="datetime-local" className="form-control" value={form.starts_at}
                onChange={(e) => setForm({ ...form, starts_at: e.target.value })} />
            </div>
            <div className="form-row">
              <label>{t('maintenance.field_end')}</label>
              <input type="datetime-local" className="form-control" value={form.ends_at}
                onChange={(e) => setForm({ ...form, ends_at: e.target.value })} />
            </div>
          </div>
          {error && <p style={{ color: 'var(--status-error)', fontSize: '0.825rem', margin: 0 }}>{error}</p>}
          <div className="form-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose}>{t('common.cancel')}</button>
            <button type="submit" className="btn btn-primary" disabled={submitting}>
              {submitting ? t('common.loading') : t('maintenance.create')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
