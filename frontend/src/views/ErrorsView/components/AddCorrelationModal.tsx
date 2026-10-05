import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiLink, HiX } from 'react-icons/hi';
import { type Host, type HostGroup, type Service } from '../../../api';
import { useOrgApi } from '../../../hooks/useOrgApi';

interface AddCorrelationModalProps {
  appName: string;
  hosts: Host[];
  servicesList: Service[];
  hostGroups: HostGroup[];
  // Every app known to the org (registered or simply observed in errors) —
  // app targets are name-based, so registration is not required.
  appNames: string[];
  onSuccess: () => void;
  onCancel: () => void;
}

export function AddCorrelationModal({
  appName,
  hosts,
  servicesList,
  hostGroups,
  appNames,
  onSuccess,
  onCancel,
}: AddCorrelationModalProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [targetType, setTargetType] = useState<'host' | 'service' | 'host_group' | 'app'>('host_group');
  const [targetId, setTargetId] = useState(0);
  const [targetAppName, setTargetAppName] = useState('');
  const otherApps = appNames.filter((name) => name !== appName);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canSubmit = targetType === 'app' ? targetAppName !== '' : targetId !== 0;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      await orgApi.links.create({
        app_service_name: appName,
        target_type: targetType,
        target_id: targetType === 'app' ? 0 : targetId,
        target_app_name: targetType === 'app' ? targetAppName : undefined,
      });
      onSuccess();
    } catch (err: any) {
      setError(err.response?.data?.error || t('errors_view.link_action_error'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-overlay" onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}>
      <div className="modal-content" style={{ maxWidth: '480px' }} onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3 className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <HiLink style={{ color: 'var(--brand-primary)' }} />
            <span>{t('errors_view.add_correlation_modal.title', { app: appName })}</span>
          </h3>
          <button className="modal-close" onClick={onCancel}>
            <HiX style={{ fontSize: '1.25rem' }} />
          </button>
        </div>
        <div className="modal-body">
          {error && <div className="error-message" style={{ marginBottom: '1rem' }}>{error}</div>}

          <div style={{ marginBottom: '1.5rem' }}>
            <label className="label">{t('errors_view.form.target_type')}</label>
            <select
              className="select"
              value={targetType}
              onChange={(e) => {
                setTargetType(e.target.value as 'host' | 'service' | 'host_group' | 'app');
                setTargetId(0);
                setTargetAppName('');
              }}>
              <option value="host_group">{t('errors_view.target_type_host_group')}</option>
              <option value="host">{t('errors_view.target_type_host')}</option>
              <option value="service">{t('errors_view.target_type_service')}</option>
              <option value="app">{t('errors_view.target_type_app')}</option>
            </select>
          </div>

          <div style={{ marginBottom: '1.5rem' }}>
            <label className="label">{t('errors_view.form.target')}</label>
            {targetType === 'app' ? (
              otherApps.length === 0 ? (
                <div style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)' }}>
                  {t('errors_view.no_other_apps')}
                </div>
              ) : (
                <select className="select" value={targetAppName} onChange={(e) => setTargetAppName(e.target.value)}>
                  <option value="">{t('errors_view.form.select')}</option>
                  {otherApps.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              )
            ) : (
              <select className="select" value={targetId || ''} onChange={(e) => setTargetId(Number(e.target.value))}>
                <option value="">{t('errors_view.form.select')}</option>
                {targetType === 'host'
                  ? hosts.map((h) => (
                      <option key={h.id} value={h.id}>
                        {h.name} ({h.host})
                      </option>
                    ))
                  : targetType === 'host_group'
                  ? hostGroups.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))
                  : servicesList.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} · {s.type} · {s.service}
                      </option>
                    ))}
              </select>
            )}
          </div>

          <div style={{ display: 'flex', gap: '0.75rem', justifyContent: 'flex-end' }}>
            <button type="button" onClick={onCancel} className="btn btn-secondary">
              {t('common.cancel')}
            </button>
            <button type="button" onClick={handleSubmit} disabled={submitting || !canSubmit} className="btn btn-primary">
              {t('errors_view.form.add')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
