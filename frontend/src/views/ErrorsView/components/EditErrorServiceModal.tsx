import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiPencil, HiX } from 'react-icons/hi';
import { HiExclamationTriangle } from 'react-icons/hi2';
import { type Service } from '../../../api';
import { useOrgApi } from '../../../hooks/useOrgApi';
import { NumberInput } from '../../../components/NumberInput';

interface EditErrorServiceModalProps {
  service: Service;
  onSuccess: (newName: string) => void;
  onCancel: () => void;
}

// Error services carry no check config: only the app name and the
// errors-per-minute alert thresholds are editable.
export function EditErrorServiceModal({
  service,
  onSuccess,
  onCancel,
}: EditErrorServiceModalProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [name, setName] = useState(service.name);
  const [warningThreshold, setWarningThreshold] = useState<number | null>(
    service.warning_threshold ?? null
  );
  const [criticalThreshold, setCriticalThreshold] = useState<number | null>(
    service.critical_threshold ?? null
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      // The update endpoint overwrites every column, so untouched fields are
      // sent back as-is.
      await orgApi.services.update(service.id, {
        name: name.trim(),
        display_name: service.display_name || undefined,
        type: service.type,
        host_id: service.host_id,
        host: service.host,
        path: service.path || undefined,
        service: service.service,
        service_interval: service.service_interval,
        max_attempts: service.max_attempts,
        warning_threshold: warningThreshold,
        critical_threshold: criticalThreshold,
      });
      onSuccess(name.trim());
    } catch (err: any) {
      setError(
        err.response?.data?.error || t('service_modal.errors.save_update')
      );
      console.error(err);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className='modal-overlay' onClick={onCancel}>
      <div
        className='modal-content'
        style={{ maxWidth: 480 }}
        onClick={(e) => e.stopPropagation()}>
        <form onSubmit={handleSubmit}>
          <div className='modal-header'>
            <span
              className='modal-title'
              style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <HiPencil style={{ color: 'var(--brand-primary)' }} />
              {t('errors_view.edit_service_title')}
            </span>
            <button type='button' className='modal-close' onClick={onCancel}>
              <HiX />
            </button>
          </div>
          <div className='modal-body'>
            <div style={{ marginBottom: '1.25rem' }}>
              <label className='label'>
                {t('errors_view.add_service_form.service_name')}
              </label>
              <input
                className='input'
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                maxLength={255}
              />
            </div>
            <div>
              <label className='label'>
                {t('service_modal.labels.alert_threshold_errors')}{' '}
                {t('service_modal.labels.alert_threshold_optional')}
              </label>
              <div style={{ display: 'flex', gap: '0.75rem' }}>
                <div style={{ flex: 1 }}>
                  <NumberInput
                    name='warning_threshold'
                    value={warningThreshold === null ? '' : warningThreshold}
                    onChange={(e) =>
                      setWarningThreshold(
                        e.target.value ? Number(e.target.value) : null
                      )
                    }
                    min={0}
                    className='input'
                    placeholder={t('service_modal.placeholders.threshold_errors')}
                  />
                  <p
                    style={{
                      color: 'var(--status-warning)',
                      fontSize: '0.7rem',
                      marginTop: '0.35rem',
                      fontWeight: 600,
                    }}>
                    <HiExclamationTriangle
                      style={{ verticalAlign: '-0.125em' }}
                    />{' '}
                    {t('service_modal.labels.threshold_warning')}
                  </p>
                </div>
                <div style={{ flex: 1 }}>
                  <NumberInput
                    name='critical_threshold'
                    value={criticalThreshold === null ? '' : criticalThreshold}
                    onChange={(e) =>
                      setCriticalThreshold(
                        e.target.value ? Number(e.target.value) : null
                      )
                    }
                    min={0}
                    className='input'
                    placeholder={t('service_modal.placeholders.threshold_errors')}
                  />
                  <p
                    style={{
                      color: 'var(--status-error)',
                      fontSize: '0.7rem',
                      marginTop: '0.35rem',
                      fontWeight: 600,
                    }}>
                    <HiExclamationTriangle
                      style={{ verticalAlign: '-0.125em' }}
                    />{' '}
                    {t('service_modal.labels.threshold_critical')}
                  </p>
                </div>
              </div>
              <p
                style={{
                  color: 'var(--text-tertiary)',
                  fontSize: '0.75rem',
                  marginTop: '0.5rem',
                }}>
                {t('service_modal.labels.threshold_hint_errors')}
              </p>
            </div>
            {error && (
              <div className='error-message' style={{ marginTop: '1rem' }}>
                {error}
              </div>
            )}
          </div>
          <div className='modal-footer'>
            <button
              type='button'
              className='btn btn-secondary'
              onClick={onCancel}>
              {t('common.cancel')}
            </button>
            <button
              type='submit'
              className='btn btn-primary'
              disabled={submitting}>
              {submitting
                ? t('service_modal.submit_updating')
                : t('service_modal.submit_update')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
