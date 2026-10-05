import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  HiOutlineBellAlert,
  HiOutlinePlus,
  HiOutlinePencil,
  HiOutlineTrash,
  HiOutlineXMark,
  HiOutlineCheckCircle,
  HiOutlineExclamationTriangle,
  HiOutlineExclamationCircle,
  HiOutlineSquares2X2,
  HiOutlineServer,
  HiOutlineGlobeAlt,
} from 'react-icons/hi2';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { TagInput } from '../components/TagInput';
import type { AlertRule, NotificationChannel, Host, ServiceWithResults } from '../api';
import { usePlan, ProUpgradeBanner } from '../components/PlanGate';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { apiErrorMessage } from '../utils/apiError';
import './AlertRulesView.css';

// Alert rules are notification ROUTING policies now: the thresholds live on the
// service. A rule picks which severities to route, to which channels, with tags.
const DEFAULT_FORM: Partial<AlertRule> = {
  name: '',
  type: 'routing',
  target_type: 'any',
  tags: '',
  notify_warning: true,
  notify_critical: true,
  enabled: true,
  channels: [],
};

export default function AlertRulesView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { isPro } = usePlan();
  const { canWrite } = useAuth();

  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<AlertRule | null>(null);
  const [form, setForm] = useState<Partial<AlertRule>>(DEFAULT_FORM);
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState<number | null>(null);
  const [error, setError] = useState('');
  // Saving happens inside the modal, so its failure has to be shown there: the
  // page behind the overlay is exactly where the user is not looking.
  const [formError, setFormError] = useState('');

  // An open form or a pending confirmation is a detail row: refreshing under
  // one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'alert-rules',
    0,
    showForm || deleteConfirm !== null,
  );

  const rulesQueryKey = [scope, 'alert-rules', 'list'];
  const rulesQuery = useQuery({
    queryKey: rulesQueryKey,
    queryFn: async () => {
      const [rulesRes, chRes, hostsRes, svcRes] = await Promise.all([
        orgApi.alertRules.list(),
        orgApi.notificationChannels.list(),
        orgApi.hosts.list().catch(() => ({ data: [] })),
        orgApi.services.list().catch(() => ({ data: [] })),
      ]);
      return {
        rules: (rulesRes.data as AlertRule[]) ?? [],
        channels: (chRes.data as NotificationChannel[]) ?? [],
        hosts: (hostsRes.data as Host[]) ?? [],
        services: (svcRes.data as ServiceWithResults[]) ?? [],
      };
    },
    refetchInterval,
  });

  const rules = rulesQuery.data?.rules ?? [];
  const channels = rulesQuery.data?.channels ?? [];
  const hosts = rulesQuery.data?.hosts ?? [];
  const services = rulesQuery.data?.services ?? [];

  const autoRefresh = buildControl({
    query: rulesQuery,
    queryKey: rulesQueryKey,
    prefix: [scope, 'alert-rules'],
  });
  const { refresh } = autoRefresh;

  const openCreate = () => {
    setEditing(null);
    setForm(DEFAULT_FORM);
    setFormError('');
    setShowForm(true);
  };

  const openEdit = (rule: AlertRule) => {
    setEditing(rule);
    setForm({ ...rule });
    setFormError('');
    setShowForm(true);
  };

  const closeForm = () => { setShowForm(false); setEditing(null); setFormError(''); };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.notify_warning && !form.notify_critical) {
      console.warn('Select at least warning or critical');
      return;
    }
    setSaving(true);
    setFormError('');
    try {
      const payload: Partial<AlertRule> = {
        name: form.name,
        type: 'routing',
        target_type: form.target_type ?? 'any',
        target_id: form.target_type !== 'any' ? form.target_id : undefined,
        tags: form.tags,
        notify_warning: form.notify_warning,
        notify_critical: form.notify_critical,
        enabled: form.enabled ?? true,
        channels: form.channels ?? [],
      };
      if (editing) {
        await orgApi.alertRules.update(editing.id, payload);
      } else {
        await orgApi.alertRules.create(payload);
      }
      closeForm();
      refresh();
    } catch (err) {
      console.error('Failed to save alert rule:', err);
      setFormError(apiErrorMessage(err, t('alert_rules.error_save')));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await orgApi.alertRules.delete(id);
      setDeleteConfirm(null);
      refresh();
    } catch (err) {
      console.error('Failed to delete alert rule:', err);
      setError(apiErrorMessage(err, t('alert_rules.error_delete')));
    }
  };

  const handleToggle = async (rule: AlertRule) => {
    try {
      await orgApi.alertRules.toggle(rule.id, !rule.enabled);
      refresh();
    } catch (err) {
      console.error('Failed to toggle alert rule:', err);
      setError(apiErrorMessage(err, t('alert_rules.error_toggle')));
    }
  };

  const toggleChannel = (id: number) => {
    const current = form.channels ?? [];
    setForm(f => ({
      ...f,
      channels: current.includes(id) ? current.filter(c => c !== id) : [...current, id],
    }));
  };

  const channelName = (id: number) => channels.find(c => c.id === id)?.name ?? `#${id}`;

  return (
    <div className="alert-rules-view">
      <div className="alert-rules-header">
        <div>
          <h1><HiOutlineBellAlert /> {t('alert_rules.title')}</h1>
          <p className="subtitle">{t('alert_rules.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button className="btn-primary" onClick={openCreate} disabled={!isPro}>
              <HiOutlinePlus /> {t('alert_rules.new_rule')}
            </button>
          )}
        </div>
      </div>

      {!isPro && (
        <div style={{ marginBottom: '1.5rem' }}>
          <ProUpgradeBanner feature={t('alert_rules.pro_feature')} />
        </div>
      )}

      {error && (
        <div className="error-message" style={{ marginBottom: '1.5rem' }}>
          <HiOutlineExclamationCircle />
          <span>{error}</span>
          <button type="button" className="error-message-close" onClick={() => setError('')}>
            <HiOutlineXMark />
          </button>
        </div>
      )}

      {/* Summary chips */}
      <div className="alert-rules-summary">
        <span className="summary-chip">{t('alert_rules.summary_rules', { count: rules.length })}</span>
        <span className="summary-chip active">{t('alert_rules.summary_active', { count: rules.filter(r => r.enabled).length })}</span>
        <span className="summary-chip critical">{t('alert_rules.summary_criticals', { count: rules.filter(r => r.notify_critical && r.enabled).length })}</span>
      </div>

      {rulesQuery.isPending ? (
        <div className="alert-rules-loading">{t('alert_rules.loading')}</div>
      ) : rules.length === 0 ? (
        <div className="empty-state">
          <HiOutlineBellAlert className="empty-state-icon" />
          <div className="empty-state-title">{t('alert_rules.empty_title')}</div>
          <div className="empty-state-description">{t('alert_rules.empty_desc')}</div>
          {canWrite && (
            <button className="empty-state-action" onClick={openCreate}>
              <HiOutlinePlus /> {t('alert_rules.create_rule')}
            </button>
          )}
        </div>
      ) : (
        <div className="alert-rules-list">
          {rules.map(rule => (
            <div key={rule.id} className={`alert-rule-card ${!rule.enabled ? 'disabled' : ''}`}>
              <div className="rule-card-top">
                <div className="rule-card-left">
                  <div className="rule-toggle-wrap">
                    <button
                      className={`rule-toggle ${rule.enabled ? 'on' : 'off'}`}
                      onClick={() => handleToggle(rule)}
                      disabled={!canWrite}
                      title={rule.enabled ? t('alert_rules.toggle_disable') : t('alert_rules.toggle_enable')}
                    />
                  </div>
                  <div>
                    <div className="rule-name">{rule.name}</div>
                    {rule.description && <div className="rule-description">{rule.description}</div>}
                  </div>
                </div>
                {canWrite && (
                  <div className="rule-card-actions">
                    <button className="btn-icon" onClick={() => openEdit(rule)} title={t('alert_rules.edit')}><HiOutlinePencil /></button>
                    <button className="btn-icon danger" onClick={() => setDeleteConfirm(rule.id)} title={t('alert_rules.delete')}><HiOutlineTrash /></button>
                  </div>
                )}
              </div>

              <div className="rule-condition">
                {rule.notify_warning && (
                  <span className="rule-pill severity-badge sev-warning"><HiOutlineExclamationTriangle style={{ verticalAlign: '-0.125em' }} /> {t('alert_rules.severity_warning')}</span>
                )}
                {rule.notify_critical && (
                  <span className="rule-pill severity-badge sev-critical"><HiOutlineExclamationCircle style={{ verticalAlign: '-0.125em' }} /> {t('alert_rules.severity_critical')}</span>
                )}
                {rule.target_type && rule.target_type !== 'any' && rule.target_id && (
                  <span className="rule-pill metric">
                    {rule.target_type === 'service'
                      ? <HiOutlineSquares2X2 style={{ verticalAlign: '-0.125em' }} />
                      : <HiOutlineServer style={{ verticalAlign: '-0.125em' }} />}{' '}
                    {rule.target_type === 'service'
                      ? (services.find(s => s.id === rule.target_id)?.name ?? `service #${rule.target_id}`)
                      : (hosts.find(h => h.id === rule.target_id)?.name ?? `host #${rule.target_id}`)}
                  </span>
                )}
                {(!rule.target_type || rule.target_type === 'any') && (
                  <span className="rule-pill" style={{ opacity: 0.55 }}><HiOutlineGlobeAlt style={{ verticalAlign: '-0.125em' }} /> {t('alert_rules.pill_global')}</span>
                )}
                {rule.tags && rule.tags.trim() !== '' && rule.tags.split(',').map((tg, i) => (
                  <span key={i} className="rule-pill metric">#{tg.trim()}</span>
                ))}
              </div>

              {rule.channels?.length > 0 && (
                <div className="rule-channels">
                  {rule.channels.map(id => (
                    <span key={id} className="rule-channel-tag">{channelName(id)}</span>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Form modal */}
      {showForm && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) closeForm(); }}>
          <div className="modal-content ar-modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">{editing ? t('alert_rules.modal_edit_title') : t('alert_rules.modal_new_title')}</h3>
              <button className="btn-close" onClick={closeForm}><HiOutlineXMark /></button>
            </div>

            <form onSubmit={handleSave} className="ar-form">
              {formError && (
                <div className="error-message" style={{ marginBottom: '1rem' }}>
                  <HiOutlineExclamationCircle />
                  <span>{formError}</span>
                </div>
              )}
              <p className="form-hint" style={{ marginBottom: '0.75rem' }}>
                {t('alert_rules.form_hint')}
              </p>
              <div className="form-row">
                <label>{t('alert_rules.name_label')}</label>
                <input value={form.name ?? ''} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder={t('alert_rules.name_placeholder')} />
              </div>

              {/* Target scope */}
              <div className="form-row">
                <label>{t('alert_rules.scope_label')}</label>
                <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
                  {(['any', 'service', 'host'] as const).map(tt => (
                    <label key={tt} style={{ display: 'inline-flex', alignItems: 'center', gap: '0.35rem', cursor: 'pointer', fontWeight: form.target_type === tt ? 700 : 400 }}>
                      <input type="radio" name="target_type" value={tt} checked={form.target_type === tt}
                        onChange={() => setForm(f => ({ ...f, target_type: tt, target_id: undefined }))} />
                      {tt === 'any' ? t('alert_rules.scope_any') : tt === 'service' ? t('alert_rules.scope_service') : t('alert_rules.scope_host')}
                    </label>
                  ))}
                </div>
                <span className="form-hint">
                  {form.target_type === 'any'
                    ? t('alert_rules.scope_hint_any')
                    : form.target_type === 'service'
                    ? t('alert_rules.scope_hint_service')
                    : t('alert_rules.scope_hint_host')}
                </span>
              </div>

              {form.target_type === 'service' && (
                <div className="form-row">
                  <label>{t('alert_rules.target_service_label')}</label>
                  <select value={form.target_id ?? ''} onChange={e => setForm(f => ({ ...f, target_id: e.target.value ? Number(e.target.value) : undefined }))}>
                    <option value="">{t('alert_rules.target_service_placeholder')}</option>
                    {services.filter(s => !s.type?.startsWith('error_service_')).map(s => (
                      <option key={s.id} value={s.id}>{s.name}{s.host_name ? ` (${s.host_name})` : ''}</option>
                    ))}
                  </select>
                </div>
              )}

              {form.target_type === 'host' && (
                <div className="form-row">
                  <label>{t('alert_rules.target_host_label')}</label>
                  <select value={form.target_id ?? ''} onChange={e => setForm(f => ({ ...f, target_id: e.target.value ? Number(e.target.value) : undefined }))}>
                    <option value="">{t('alert_rules.target_host_placeholder')}</option>
                    {hosts.map(h => (
                      <option key={h.id} value={h.id}>{h.display_name || h.name}</option>
                    ))}
                  </select>
                </div>
              )}

              <div className="form-row">
                <label>{t('alert_rules.severities_label')}</label>
                <div style={{ display: 'flex', gap: '1.25rem', marginTop: '0.25rem' }}>
                  <label style={{ display: 'inline-flex', alignItems: 'center', gap: '0.4rem', cursor: 'pointer', color: 'var(--status-warning)', fontWeight: 600 }}>
                    <input type="checkbox" checked={form.notify_warning ?? false}
                      onChange={e => setForm(f => ({ ...f, notify_warning: e.target.checked }))} />
                    <HiOutlineExclamationTriangle /> {t('alert_rules.severity_warning')}
                  </label>
                  <label style={{ display: 'inline-flex', alignItems: 'center', gap: '0.4rem', cursor: 'pointer', color: 'var(--status-error)', fontWeight: 600 }}>
                    <input type="checkbox" checked={form.notify_critical ?? false}
                      onChange={e => setForm(f => ({ ...f, notify_critical: e.target.checked }))} />
                    <HiOutlineExclamationCircle /> {t('alert_rules.severity_critical')}
                  </label>
                </div>
              </div>

              <div className="form-row">
                <label>{t('alert_rules.tags_label')}</label>
                <TagInput value={form.tags ?? ''} onChange={tags => setForm(f => ({ ...f, tags }))} placeholder={t('alert_rules.tags_placeholder')} />
                <span className="form-hint">{t('alert_rules.tags_hint')}</span>
              </div>

              {channels.length > 0 && (
                <div className="form-row">
                  <label>{t('alert_rules.channels_label')}</label>
                  <div className="channel-picker">
                    {channels.map(ch => {
                      const selected = (form.channels ?? []).includes(ch.id);
                      return (
                        <button
                          key={ch.id}
                          type="button"
                          className={`channel-chip ${selected ? 'selected' : ''}`}
                          onClick={() => toggleChannel(ch.id)}
                        >
                          {selected && <HiOutlineCheckCircle />} {ch.name}
                          <span className="channel-type-tag">{ch.type}</span>
                        </button>
                      );
                    })}
                  </div>
                  <span className="form-hint">{t('alert_rules.channels_hint')}</span>
                </div>
              )}

              {channels.length === 0 && (
                <div className="ar-channels-warning">
                  <HiOutlineExclamationTriangle />
                  {t('alert_rules.no_channel_before')}<a href="../alerts/channels">{t('alert_rules.no_channel_link')}</a>{t('alert_rules.no_channel_after')}
                </div>
              )}

              <div className="modal-footer" style={{ marginTop: '1.5rem' }}>
                <button type="button" className="btn btn-secondary" onClick={closeForm}>{t('alert_rules.cancel')}</button>
                <button type="submit" className="btn btn-primary" disabled={saving}>
                  {saving ? t('alert_rules.saving') : editing ? t('alert_rules.update') : t('alert_rules.create')}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
      {deleteConfirm !== null && (
        <ConfirmDialog
          title={t('alert_rules.delete_title')}
          message={t('alert_rules.delete_message')}
          onConfirm={() => handleDelete(deleteConfirm)}
          onCancel={() => setDeleteConfirm(null)}
        />
      )}
    </div>
  );
}
