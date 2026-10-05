import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  HiOutlineBell,
  HiOutlinePlus,
  HiOutlineTrash,
  HiOutlinePencil,
  HiOutlineXMark,
  HiOutlinePaperAirplane,
  HiOutlineCheckCircle,
  HiOutlineExclamationCircle,
} from 'react-icons/hi2';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { usePlan, ProUpgradeBanner } from '../components/PlanGate';
import type { NotificationChannel } from '../api';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { TagInput } from '../components/TagInput';
import { apiErrorMessage } from '../utils/apiError';
import './NotificationChannelsView.css';

const CHANNEL_TYPE_VALUES = ['email', 'slack', 'jsm', 'whatsapp', 'webhook'] as const;

function NotificationChannelsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const { isPro } = usePlan();
  const [showForm, setShowForm] = useState(false);
  const [editingChannel, setEditingChannel] = useState<NotificationChannel | null>(null);
  const [pendingDeleteId, setPendingDeleteId] = useState<number | null>(null);

  const [formData, setFormData] = useState({
    name: '',
    type: 'email',
    enabled: true,
  });

  const [configData, setConfigData] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [testingId, setTestingId] = useState<number | null>(null);
  const [testResult, setTestResult] = useState<{ id: number; ok: boolean; message?: string } | null>(null);
  const [error, setError] = useState('');

  const channelTypes = useMemo(
    () =>
      CHANNEL_TYPE_VALUES.map((value) => ({
        value,
        label: t(`channels.types.${value}`),
      })),
    [t]
  );

  const getNamePlaceholder = (type: string) => {
    switch (type) {
      case 'email':
        return t('channels.form.name_placeholder_email');
      case 'slack':
        return t('channels.form.name_placeholder_slack');
      case 'webhook':
        return t('channels.form.name_placeholder_webhook');
      case 'jsm':
        return t('channels.form.name_placeholder_jsm');
      case 'whatsapp':
        return t('channels.form.name_placeholder_whatsapp');
      default:
        return t('channels.form.name_placeholder_default');
    }
  };

  // An open form or a pending confirmation is a detail row: refreshing under
  // one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'notification-channels',
    0,
    showForm || pendingDeleteId !== null,
  );

  const channelsQueryKey = [scope, 'notification-channels', 'list'];
  const channelsQuery = useQuery({
    queryKey: channelsQueryKey,
    queryFn: async () => ((await orgApi.notificationChannels.list()).data as NotificationChannel[]) || [],
    refetchInterval,
  });

  const channels = channelsQuery.data ?? [];

  const autoRefresh = buildControl({
    query: channelsQuery,
    queryKey: channelsQueryKey,
    prefix: [scope, 'notification-channels'],
  });
  const { refresh } = autoRefresh;

  const resetForm = () => {
    setFormData({ name: '', type: 'email', enabled: true });
    setConfigData({});
    setEditingChannel(null);
    setShowForm(false);
    setFormError(null);
    setFieldErrors({});
  };

  const EMAIL_RE = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;

  const validateChannelField = (field: string, value: string): string => {
    if (field === 'name') {
      if (!value.trim()) return t('channels.form.error_name_required');
      if (value.length > 255) return t('channels.form.error_name_too_long');
    }
    if (field === 'emails') {
      const emails = value.split(',').map(e => e.trim()).filter(Boolean);
      if (!emails.length) return t('channels.form.error_emails_required');
      for (const email of emails) {
        if (!EMAIL_RE.test(email)) return t('channels.form.error_email_invalid', { email });
      }
    }
    if (field === 'webhook_url') {
      if (!value) return t('channels.form.error_webhook_required');
      try { new URL(value); } catch { return t('channels.form.error_webhook_invalid'); }
    }
    if (field === 'phone_number') {
      if (!value) return t('channels.form.error_phone_required');
      if (!/^\+?[1-9]\d{6,14}$/.test(value.replace(/\s/g, '')))
        return t('channels.form.error_phone_invalid');
    }
    return '';
  };

  const handleFieldBlur = (field: string, value: string) => {
    const err = validateChannelField(field, value);
    setFieldErrors(prev => ({ ...prev, [field]: err }));
  };

  const clearFieldError = (field: string) => {
    if (fieldErrors[field]) setFieldErrors(prev => ({ ...prev, [field]: '' }));
  };

  const validateChannelForm = (): string | null => {
    if (!formData.name.trim()) return t('channels.form.error_name_required');
    if (formData.name.length > 255) return t('channels.form.error_name_too_long');

    if (formData.type === 'email') {
      const emails = (configData.emails || '').split(',').map(e => e.trim()).filter(Boolean);
      if (!emails.length) return t('channels.form.error_emails_required');
      const emailRe = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
      for (const email of emails) {
        if (!emailRe.test(email)) return t('channels.form.error_email_invalid', { email });
      }
    }

    if (formData.type === 'slack' || formData.type === 'webhook') {
      const url = configData.webhook_url || '';
      if (!url) return t('channels.form.error_webhook_required');
      try { new URL(url); } catch { return t('channels.form.error_webhook_invalid'); }
    }

    if (formData.type === 'whatsapp') {
      const phone = configData.phone_number || '';
      if (!phone) return t('channels.form.error_phone_required');
      if (!/^\+?[1-9]\d{6,14}$/.test(phone.replace(/\s/g, '')))
        return t('channels.form.error_phone_invalid');
      if (!(configData.phone_number_id || '').trim())
        return t('channels.form.error_phone_number_id_required');
    }

    return null;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    const globalErr = validateChannelForm();
    if (globalErr) { setFormError(globalErr); return; }
    try {
      const payload = {
        name: formData.name,
        type: formData.type,
        enabled: formData.enabled,
        config: configData,
      };

      if (editingChannel) {
        await orgApi.notificationChannels.update(editingChannel.id, payload);
      } else {
        await orgApi.notificationChannels.create(payload);
      }
      resetForm();
      refresh();
    } catch (err) {
      console.error('Failed to save notification channel:', err);
      setFormError(apiErrorMessage(err, t('channels.error_save')));
    }
  };

  const handleEdit = (channel: NotificationChannel) => {
    setFormData({
      name: channel.name,
      type: channel.type,
      enabled: channel.enabled,
    });
    setConfigData(channel.config || {});
    setEditingChannel(channel);
    setShowForm(true);
  };

  const handleDelete = async (id: number) => {
    try {
      await orgApi.notificationChannels.delete(id);
      refresh();
    } catch (err) {
      console.error('Failed to delete notification channel:', err);
      setError(apiErrorMessage(err, t('channels.error_delete')));
    } finally {
      setPendingDeleteId(null);
    }
  };

  const handleTest = async (channel: NotificationChannel) => {
    setTestingId(channel.id);
    setTestResult(null);
    try {
      await orgApi.notificationChannels.test(channel.id);
      setTestResult({ id: channel.id, ok: true });
    } catch (err) {
      console.error('Channel test failed:', err);
      // The server says why the delivery failed (missing SMTP, refused webhook);
      // a generic "check the configuration" sends the user looking in the wrong
      // place.
      setTestResult({ id: channel.id, ok: false, message: apiErrorMessage(err, t('channels.test_failed')) });
    } finally {
      setTestingId(null);
      setTimeout(() => setTestResult(null), 10000);
    }
  };

  const handleToggle = async (channel: NotificationChannel) => {
    try {
      await orgApi.notificationChannels.update(channel.id, {
        name: channel.name,
        type: channel.type,
        config: channel.config,
        enabled: !channel.enabled,
      });
      refresh();
    } catch (err) {
      console.error('Failed to toggle notification channel:', err);
      setError(apiErrorMessage(err, t('channels.error_toggle')));
    }
  };

  const renderConfigFields = () => {
    switch (formData.type) {
      case 'email':
        return (
          <div style={{ marginBottom: '1.25rem' }}>
            <label className="label">{t('channels.config.emails')}</label>
            <TagInput
              value={configData.emails || ''}
              onChange={emails => { setConfigData({ ...configData, emails }); clearFieldError('emails'); }}
              onBlur={emails => handleFieldBlur('emails', emails)}
              placeholder={t('channels.config.emails_placeholder')}
              invalid={!!fieldErrors.emails}
            />
            {fieldErrors.emails && <p className="field-error">{fieldErrors.emails}</p>}
          </div>
        );
      case 'slack':
      case 'webhook':
        return (
          <>
            <div style={{ marginBottom: '1.25rem' }}>
              <label className="label">{t('channels.config.webhook_url')}</label>
              <input
                type="text"
                required
                value={configData.webhook_url || ''}
                onChange={e => { setConfigData({ ...configData, webhook_url: e.target.value }); clearFieldError('webhook_url'); }}
                onBlur={e => handleFieldBlur('webhook_url', e.target.value)}
                placeholder={t('channels.config.webhook_placeholder')}
                className={`input${fieldErrors.webhook_url ? ' input-invalid' : ''}`}
              />
              {fieldErrors.webhook_url && <p className="field-error">{fieldErrors.webhook_url}</p>}
            </div>
            {formData.type === 'webhook' && (
              <div style={{ marginBottom: '1.25rem' }}>
                <label className="label">{t('channels.config.webhook_secret')}</label>
                <input
                  type="password"
                  value={configData.secret || ''}
                  onChange={e => setConfigData({ ...configData, secret: e.target.value })}
                  className="input"
                />
                <span className="form-hint">{t('channels.config.webhook_secret_hint')}</span>
              </div>
            )}
          </>
        );
      case 'jsm':
        return (
          <div style={{ marginBottom: '1.25rem' }}>
            <label className="label">{t('channels.config.jsm_api_key')}</label>
            <input
              type="password"
              required
              value={configData.api_key || ''}
              onChange={e => setConfigData({ ...configData, api_key: e.target.value })}
              className="input"
            />
            <span className="form-hint">{t('channels.config.jsm_api_key_hint')}</span>
          </div>
        );
      case 'whatsapp':
        return (
          <>
            <div style={{ marginBottom: '1.25rem' }}>
              <label className="label">{t('channels.config.phone_number')}</label>
              <input
                type="text"
                required
                value={configData.phone_number || ''}
                onChange={e => { setConfigData({ ...configData, phone_number: e.target.value }); clearFieldError('phone_number'); }}
                onBlur={e => handleFieldBlur('phone_number', e.target.value)}
                placeholder={t('channels.config.phone_placeholder')}
                className={`input${fieldErrors.phone_number ? ' input-invalid' : ''}`}
              />
              {fieldErrors.phone_number && <p className="field-error">{fieldErrors.phone_number}</p>}
            </div>
            <div style={{ marginBottom: '1.25rem' }}>
              <label className="label">{t('channels.config.phone_number_id')}</label>
              <input
                type="text"
                required
                value={configData.phone_number_id || ''}
                onChange={e => { setConfigData({ ...configData, phone_number_id: e.target.value }); clearFieldError('phone_number_id'); }}
                className={`input${fieldErrors.phone_number_id ? ' input-invalid' : ''}`}
              />
              <span className="form-hint">{t('channels.config.phone_number_id_hint')}</span>
              {fieldErrors.phone_number_id && <p className="field-error">{fieldErrors.phone_number_id}</p>}
            </div>
            <div style={{ marginBottom: '1.25rem' }}>
              <label className="label">{t('channels.config.whatsapp_token')}</label>
              <input
                type="password"
                required
                value={configData.token || ''}
                onChange={e => setConfigData({ ...configData, token: e.target.value })}
                className="input"
              />
            </div>
          </>
        );
      default:
        return null;
    }
  };

  const getConfigSummary = (channel: NotificationChannel) => {
    if (channel.type === 'email') {
      return channel.config.emails || t('channels.card.emails_configured');
    }
    if (channel.type === 'slack' || channel.type === 'webhook') {
      return t('channels.card.webhook_configured');
    }
    return t('channels.card.credentials_configured');
  };

  return (
    <div className="notification-channels-view">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '2rem' }}>
        <div>
          <h1 className="page-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <HiOutlineBell /> {t('channels.title')}
          </h1>
          <p className="page-subtitle">{t('channels.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button className="btn btn-primary" onClick={() => setShowForm(true)} disabled={!isPro}>
              <HiOutlinePlus /> {t('channels.new_channel')}
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="error-message" style={{ marginBottom: '1.5rem' }}>
          <HiOutlineExclamationCircle />
          <span>{error}</span>
          <button type="button" className="error-message-close" onClick={() => setError('')}>
            <HiOutlineXMark />
          </button>
        </div>
      )}

      {!isPro && (
        <div style={{ marginBottom: '1.5rem' }}>
          <ProUpgradeBanner feature={t('channels.title')} />
        </div>
      )}

      {showForm && (
        <div className="modal-overlay" onClick={(e) => { if (e.target === e.currentTarget) resetForm(); }}>
          <div className="modal-content" style={{ maxWidth: '500px' }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">{editingChannel ? t('channels.modal.edit_title') : t('channels.modal.create_title')}</h3>
              <button className="btn-icon" onClick={resetForm}><HiOutlineXMark /></button>
            </div>
            <form onSubmit={handleSubmit} style={{ padding: '1.5rem' }}>
              {formError && (
                <div className="error-message" style={{ marginBottom: '1rem' }}>
                  {formError}
                </div>
              )}
              <div style={{ marginBottom: '1.25rem' }}>
                <label className="label">{t('channels.form.name')}</label>
                <input
                  type="text"
                  required
                  value={formData.name}
                  onChange={e => { setFormData({ ...formData, name: e.target.value }); clearFieldError('name'); }}
                  onBlur={e => handleFieldBlur('name', e.target.value)}
                  placeholder={getNamePlaceholder(formData.type)}
                  className={`input${fieldErrors.name ? ' input-invalid' : ''}`}
                />
                {fieldErrors.name && <p className="field-error">{fieldErrors.name}</p>}
              </div>

              <div style={{ marginBottom: '1.25rem' }}>
                <label className="label">{t('channels.form.channel_type')}</label>
                <select
                  value={formData.type}
                  onChange={e => {
                    setFormData({ ...formData, type: e.target.value });
                    setConfigData({});
                  }}
                  className="input"
                >
                  {channelTypes.map((channelType) => (
                    <option key={channelType.value} value={channelType.value}>{channelType.label}</option>
                  ))}
                </select>
              </div>

              {renderConfigFields()}

              <div style={{ marginBottom: '1.5rem', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                <input
                  type="checkbox"
                  id="enabled-checkbox"
                  checked={formData.enabled}
                  onChange={e => setFormData({ ...formData, enabled: e.target.checked })}
                  style={{ width: '16px', height: '16px', cursor: 'pointer' }}
                />
                <label htmlFor="enabled-checkbox" style={{ cursor: 'pointer', fontWeight: 500, color: 'var(--text-primary)' }}>
                  {t('channels.form.enable')}
                </label>
              </div>

              <div className="modal-footer" style={{ marginTop: '2rem', padding: 0, borderTop: 'none' }}>
                <button type="button" className="btn btn-secondary" onClick={resetForm}>
                  {t('common.cancel')}
                </button>
                <button type="submit" className="btn btn-primary">
                  {editingChannel ? t('common.save') : t('common.create')}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      <div>
        {channelsQuery.isPending ? (
          <div className="loading" style={{ padding: '3rem', textAlign: 'center', color: 'var(--text-tertiary)' }}>
            {t('channels.loading_channels')}
          </div>
        ) : channels.length === 0 ? (
          <div className="empty-state">
            <HiOutlineBell className="empty-state-icon" />
            <div className="empty-state-title">{t('channels.empty_title')}</div>
            <div className="empty-state-description">{t('channels.empty_desc')}</div>
          </div>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(350px, 1fr))', gap: '1rem' }}>
            {channels.map(channel => (
              <div key={channel.id} className="card" style={{ opacity: channel.enabled ? 1 : 0.6, transition: 'opacity 0.2s' }}>
                <div className="card-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingBottom: '1rem', borderBottom: '1px solid var(--border-primary)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                    <h3 className="card-title" style={{ margin: 0 }}>{channel.name}</h3>
                    <span className={`status-badge status-${channel.enabled ? 'success' : 'unknown'}`} style={{ fontSize: '0.7rem', padding: '2px 6px' }}>
                      {channel.enabled ? t('common.enabled') : t('common.disabled')}
                    </span>
                  </div>
                  {canWrite && (
                    <div style={{ display: 'flex', gap: '0.25rem' }}>
                      <button className="btn-icon" onClick={() => handleTest(channel)} disabled={testingId === channel.id} title={t('channels.test_button')}>
                        <HiOutlinePaperAirplane />
                      </button>
                      <button className="btn-icon" onClick={() => handleToggle(channel)} title={channel.enabled ? t('common.deactivate') : t('common.activate')}>
                        <HiOutlineBell />
                      </button>
                      <button className="btn-icon" onClick={() => handleEdit(channel)} title={t('common.edit')}>
                        <HiOutlinePencil />
                      </button>
                      <button className="btn-icon" style={{ color: 'var(--status-error)' }} onClick={() => setPendingDeleteId(channel.id)} title={t('common.delete')}>
                        <HiOutlineTrash />
                      </button>
                    </div>
                  )}
                </div>
                <div className="card-body" style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', fontSize: '0.875rem' }}>
                  {(testingId === channel.id || testResult?.id === channel.id) && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', fontSize: '0.8rem', color: testResult ? (testResult.ok ? 'var(--status-success)' : 'var(--status-error)') : 'var(--text-secondary)' }}>
                      {testingId === channel.id ? (
                        <>{t('channels.test_sending')}</>
                      ) : testResult?.ok ? (
                        <><HiOutlineCheckCircle /> {t('channels.test_sent')}</>
                      ) : (
                        <><HiOutlineExclamationCircle /> {testResult?.message || t('channels.test_failed')}</>
                      )}
                    </div>
                  )}
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ color: 'var(--text-secondary)' }}>{t('common.type')}</span>
                    <span style={{ fontWeight: 500, color: 'var(--text-primary)' }}>{channelTypes.find(ct => ct.value === channel.type)?.label || channel.type}</span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ color: 'var(--text-secondary)' }}>{t('common.configuration')}</span>
                    <span style={{ color: 'var(--text-primary)', maxWidth: '200px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={channel.type === 'email' ? channel.config.emails : undefined}>
                      {getConfigSummary(channel)}
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      {pendingDeleteId !== null && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('channels.delete_confirm')}
          onConfirm={() => handleDelete(pendingDeleteId)}
          onCancel={() => setPendingDeleteId(null)}
        />
      )}
    </div>
  );
}

export default NotificationChannelsView;
