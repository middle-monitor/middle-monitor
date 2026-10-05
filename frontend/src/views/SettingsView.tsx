import { useState, useEffect } from 'react';
import { useSearchParams, Navigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useOrgPath } from '../hooks/useOrgPath';
import {
  HiUserGroup,
  HiPencil,
  HiTrash,
  HiPlus,
  HiXMark,
  HiCheck,
  HiExclamationCircle,
  HiShieldCheck,
  HiOutlineExclamationTriangle,
} from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { usePlan } from '../components/PlanGate';
import { CustomPlanCalculator, type CustomPlanValues } from '../components/CustomPlanCalculator';
import { User, OrganizationStats, Organization } from '../api';
import { useOrgApi } from '../hooks/useOrgApi';
import { ConfirmDialog } from '../components/ConfirmDialog';
import APIKeysView from './APIKeysView';
import './SettingsView.css';

export default function SettingsView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const { user, organization, refreshUser, isAdmin } = useAuth();
  const { isPaidSubscriber: isPaidPlan, isTrial, trialDaysLeft, billingEnabled } = usePlan();
  const { orgPath } = useOrgPath();
  const [users, setUsers] = useState<User[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [showInviteModal, setShowInviteModal] = useState(false);
  const [showEditPlanModal, setShowEditPlanModal] = useState(false);
  const [pendingDeleteUserId, setPendingDeleteUserId] = useState<number | null>(null);
  const [editingOrg, setEditingOrg] = useState(false);
  const [orgName, setOrgName] = useState(organization?.name || '');
  const [orgStats, setOrgStats] = useState<OrganizationStats | null>(null);
  const [isCheckoutLoading, setIsCheckoutLoading] = useState(false);
  const [isPortalLoading, setIsPortalLoading] = useState(false);
  const [success, setSuccess] = useState('');
  const [mfaRequired, setMfaRequired] = useState(organization?.mfa_required ?? false);
  const [smtpHost, setSmtpHost] = useState(organization?.smtp_host || '');
  const [smtpPort, setSmtpPort] = useState(organization?.smtp_port || '');
  const [smtpUser, setSmtpUser] = useState(organization?.smtp_user || '');
  const [smtpFrom, setSmtpFrom] = useState(organization?.smtp_from || '');
  const [smtpPass, setSmtpPass] = useState('');
  const [smtpConfigured, setSmtpConfigured] = useState(organization?.smtp_configured ?? false);
  const [isSavingSmtp, setIsSavingSmtp] = useState(false);
  const [isTestingSmtp, setIsTestingSmtp] = useState(false);
  const [searchParams, setSearchParams] = useSearchParams();

  useEffect(() => {
    // Listing org users is admin-only; non-admins skip the (forbidden) fetch.
    if (isAdmin) {
      fetchUsers();
    } else {
      setIsLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin]);

  useEffect(() => {
    const checkout = searchParams.get('checkout');
    if (checkout === 'success') {
      refreshUser();
      setSuccess(t('settings.checkout_success'));
      searchParams.delete('checkout');
      setSearchParams(searchParams, { replace: true });
    } else if (checkout === 'cancelled') {
      setError(t('settings.checkout_cancelled'));
      searchParams.delete('checkout');
      setSearchParams(searchParams, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    setOrgName(organization?.name || '');
    setMfaRequired(organization?.mfa_required ?? false);
    setSmtpHost(organization?.smtp_host || '');
    setSmtpPort(organization?.smtp_port || '');
    setSmtpUser(organization?.smtp_user || '');
    setSmtpFrom(organization?.smtp_from || '');
    setSmtpConfigured(organization?.smtp_configured ?? false);
  }, [organization]);

  const fetchStats = () => {
    orgApi.getStats().then((r) => setOrgStats(r.data)).catch(() => {});
  };

  useEffect(() => {
    fetchStats();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleToggleMfa = async (value: boolean) => {
    // Optimistic UI; revert on failure. Enforcing 2FA forces every member (including
    // the admin who toggled it) into the enrollment gate on their next dashboard load.
    setMfaRequired(value);
    try {
      await orgApi.update({ mfa_required: value });
      await refreshUser();
    } catch (err: any) {
      // 409 means the admin has no authenticator yet: enforcing now would lock them out.
      setError(
        err.response?.status === 409
          ? t('settings.mfa_enroll_first')
          : err.response?.data?.error || t('settings.error_update_org')
      );
      setMfaRequired(!value);
    }
  };

  const fetchUsers = async () => {
    try {
      const response = await orgApi.getUsers();
      setUsers(response.data);
    } catch (err) {
      setError(t('settings.error_load_users'));
    } finally {
      setIsLoading(false);
    }
  };

  const handleUpdateOrg = async () => {
    try {
      await orgApi.update({ name: orgName });
      await refreshUser();
      setEditingOrg(false);
    } catch (err) {
      setError(t('settings.error_update_org'));
    }
  };

  const handleChangeRole = async (userId: number, role: string) => {
    const prev = users;
    // Optimistic update; revert on failure.
    setUsers((list) => list.map((u) => (u.id === userId ? { ...u, role } : u)));
    setError('');
    setSuccess('');
    try {
      await orgApi.updateUserRole(userId, role);
      setSuccess(t('settings.role_updated'));
    } catch (err: any) {
      setUsers(prev);
      setError(err.response?.data?.error || t('settings.error_update_role'));
    }
  };

  const handleDeleteUser = async (userId: number) => {
    try {
      await orgApi.deleteUser(userId);
      setUsers(users.filter((u) => u.id !== userId));
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.error_remove_user'));
    } finally {
      setPendingDeleteUserId(null);
    }
  };

  const handleUpgradeToPro = async () => {
    if (!organization) return;
    setIsCheckoutLoading(true);
    setError('');
    setSuccess('');
    try {
      const response = await orgApi.payments.checkout({ plan: 'pro' });
      window.location.href = response.data.url;
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.error_checkout'));
      setIsCheckoutLoading(false);
    }
  };

  const handleSaveSmtp = async () => {
    setIsSavingSmtp(true);
    setError('');
    setSuccess('');
    try {
      const payload: Partial<Organization> = {
        smtp_host: smtpHost.trim(),
        smtp_port: smtpPort.trim(),
        smtp_user: smtpUser,
        smtp_from: smtpFrom.trim(),
      };
      if (smtpPass) payload.smtp_pass = smtpPass;
      await orgApi.update(payload);
      await refreshUser();
      if (smtpPass) setSmtpConfigured(true);
      setSmtpPass('');
      setSuccess(t('settings.smtp_saved'));
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.error_update_org'));
    } finally {
      setIsSavingSmtp(false);
    }
  };

  const handleTestSmtp = async () => {
    setIsTestingSmtp(true);
    setError('');
    setSuccess('');
    try {
      const res = await orgApi.testSmtp();
      setSuccess(t('settings.smtp_test_sent', { email: res.data.to }));
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.smtp_test_failed'));
    } finally {
      setIsTestingSmtp(false);
    }
  };

  const handleManageSubscription = async () => {
    setIsPortalLoading(true);
    setError('');
    setSuccess('');
    try {
      const response = await orgApi.payments.billingPortal();
      window.location.href = response.data.url;
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.error_portal'));
      setIsPortalLoading(false);
    }
  };

  // Org settings are admin-only. Non-admins are redirected to the dashboard
  // (the nav link is also hidden for them).
  if (!isAdmin) {
    return <Navigate to={orgPath('/')} replace />;
  }

  return (
    <div className='settings-view'>
      <header className='view-header settings-header'>
        <div>
          <h1>{t('settings.title')}</h1>
          <p className="subtitle">{t('settings.subtitle')}</p>
        </div>
      </header>

      {success && (
        <div className='settings-success'>
          <HiCheck />
          <span>{success}</span>
          <button onClick={() => setSuccess('')}>
            <HiXMark />
          </button>
        </div>
      )}

      {error && (
        <div className='settings-error'>
          <HiExclamationCircle />
          <span>{error}</span>
          <button onClick={() => setError('')}>
            <HiXMark />
          </button>
        </div>
      )}

      {/* Organization Section */}
      <section className='settings-section'>
        <div className='section-header'>
          <h2>{t('settings.section_org')}</h2>
        </div>

        <div className='org-card'>
          <div className='org-info'>
            {editingOrg ? (
              <div className='org-edit-form'>
                <input
                  type='text'
                  value={orgName}
                  onChange={(e) => setOrgName(e.target.value)}
                  placeholder={t('settings.org_name_placeholder')}
                />
                <div className='org-edit-actions'>
                  <button className='btn-icon success' onClick={handleUpdateOrg}>
                    <HiCheck />
                  </button>
                  <button className='btn-icon' onClick={() => setEditingOrg(false)}>
                    <HiXMark />
                  </button>
                </div>
              </div>
            ) : (
              <>
                <div>
                  <h3>{organization?.name}</h3>
                  <p className='org-slug'>{t('settings.slug')}: {organization?.slug}</p>
                </div>
                {isAdmin && (
                  <button className='btn-icon' onClick={() => setEditingOrg(true)}>
                    <HiPencil />
                  </button>
                )}
              </>
            )}
          </div>
        </div>
      </section>

      {/* Security / Two-factor Section */}
      <section className='settings-section'>
        <div className='section-header'>
          <h2><HiShieldCheck /> {t('settings.section_security')}</h2>
        </div>
        <div className='org-card'>
          <p className='text-sm' style={{ color: 'var(--text-secondary)', margin: '0 0 1rem' }}>
            {t('settings.mfa_desc')}
          </p>
          <label style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0.5rem 0', cursor: isAdmin ? 'pointer' : 'not-allowed' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', fontWeight: 600 }}>
              {t('settings.mfa_required_label')}
            </span>
            <input
              type='checkbox'
              checked={mfaRequired}
              disabled={!isAdmin}
              onChange={(e) => handleToggleMfa(e.target.checked)}
              style={{ width: 18, height: 18, cursor: isAdmin ? 'pointer' : 'not-allowed', accentColor: 'var(--brand-primary)' }}
            />
          </label>
          {!isAdmin && (
            <p className='text-sm' style={{ color: 'var(--text-tertiary)', margin: '0.25rem 0 0' }}>
              {t('settings.mfa_admin_only')}
            </p>
          )}
          <p className='text-sm' style={{ color: 'var(--text-tertiary)', margin: '0.75rem 0 0', display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
            {user?.totp_enabled ? <HiCheck style={{ color: 'var(--status-success)' }} /> : <HiExclamationCircle style={{ color: 'var(--status-warning)' }} />}
            {user?.totp_enabled ? t('settings.mfa_self_enrolled') : t('settings.mfa_self_not_enrolled')}
          </p>
        </div>
      </section>

      {/* API access: org API tokens + agent install tokens. */}
      <section className='settings-section'>
        <APIKeysView />
      </section>

      {/* Billing Section */}
      {billingEnabled && (
      <section className='settings-section' id='billing'>
        <div className='section-header'>
          <h2>{t('settings.section_billing')}</h2>
        </div>
        <div className='org-card'>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <div>
              <h3>{t('settings.current_plan')}: <strong style={{ textTransform: 'capitalize' }}>{organization?.plan || 'free'}</strong></h3>
              <p className='text-sm' style={{ color: 'var(--text-color-secondary)', marginTop: '4px' }}>
                {isTrial
                  ? t('settings.plan_trial', { count: trialDaysLeft })
                  : isPaidPlan
                    ? t('settings.plan_pro_active')
                    : t('settings.plan_pro_upsell')}
              </p>
            </div>
            {isAdmin && !isPaidPlan && (
              <button
                className='btn-primary'
                onClick={handleUpgradeToPro}
                disabled={isCheckoutLoading}
              >
                {isCheckoutLoading ? t('settings.loading_plan') : t('settings.upgrade_pro')}
              </button>
            )}
            {isAdmin && isPaidPlan && (
              <div style={{ display: 'flex', gap: '8px' }}>
                <button
                  className='btn-primary'
                  onClick={() => setShowEditPlanModal(true)}
                >
                  <HiPencil /> {t('settings.adjust_plan')}
                </button>
                <button
                  className='btn-secondary'
                  onClick={handleManageSubscription}
                  disabled={isPortalLoading}
                >
                  {isPortalLoading ? t('settings.loading_plan') : t('settings.manage_subscription')}
                </button>
              </div>
            )}
          </div>
          {orgStats && (
            <div className='plan-usage'>
              <p className='plan-usage-title'>{t('settings.usage_title')}</p>
              <PlanUsageBar
                label={t('settings.usage_hosts')}
                used={orgStats.plan_usage.hosts_used}
                limit={orgStats.plan_usage.hosts_limit}
                unlimited={t('settings.usage_unlimited')}
                of={t('settings.usage_of')}
              />
              <PlanUsageBar
                label={t('settings.usage_services')}
                used={orgStats.plan_usage.services_used}
                limit={orgStats.plan_usage.services_limit}
                unlimited={t('settings.usage_unlimited')}
                of={t('settings.usage_of')}
              />
              <PlanUsageBar
                label={t('settings.usage_error_services')}
                used={orgStats.plan_usage.error_services_used}
                limit={orgStats.plan_usage.error_services_limit}
                unlimited={t('settings.usage_unlimited')}
                of={t('settings.usage_of')}
              />
              {/* Absent until the API that meters ingestion is deployed. */}
              {orgStats.plan_usage.points_per_minute_limit !== undefined && (
                <PlanUsageBar
                  label={t('settings.usage_points')}
                  used={orgStats.plan_usage.points_per_minute_peak}
                  limit={orgStats.plan_usage.points_per_minute_limit}
                  unlimited={t('settings.usage_unlimited')}
                  of={t('settings.usage_of')}
                  format={(n) => n.toLocaleString(i18n.language)}
                />
              )}
              {orgStats.plan_usage.points_over_limit_24h > 0 && (
                <p className='usage-rejected'>
                  <HiOutlineExclamationTriangle aria-hidden='true' />
                  <span>
                    {t(
                      orgStats.plan_usage.points_limit_enforced
                        ? 'settings.usage_points_rejected'
                        : 'settings.usage_points_over',
                      { formatted: orgStats.plan_usage.points_over_limit_24h.toLocaleString(i18n.language) },
                    )}{' '}
                    <a href='/docs#limits'>{t('settings.usage_points_help')}</a>
                  </span>
                </p>
              )}
            </div>
          )}
        </div>
      </section>
      )}

      {/* SMTP Section — email alert channels send through the org's own SMTP server. */}
      <section className='settings-section'>
        <div className='section-header'>
          <h2>{t('settings.section_smtp')}</h2>
        </div>
        <div className='org-card'>
          <p className='text-sm' style={{ color: 'var(--text-secondary)', margin: '0 0 1rem' }}>
            {t('settings.smtp_desc')}
          </p>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
            <div>
              <label className='label'>{t('settings.smtp_host')}</label>
              <input className='input' type='text' value={smtpHost} placeholder='smtp.example.com'
                onChange={(e) => setSmtpHost(e.target.value)} />
            </div>
            <div>
              <label className='label'>{t('settings.smtp_port')}</label>
              <input className='input' type='text' value={smtpPort} placeholder='587'
                onChange={(e) => setSmtpPort(e.target.value)} />
            </div>
            <div>
              <label className='label'>{t('settings.smtp_user')}</label>
              <input className='input' type='text' value={smtpUser} autoComplete='off'
                onChange={(e) => setSmtpUser(e.target.value)} />
            </div>
            <div>
              <label className='label'>{t('settings.smtp_pass')}</label>
              <input className='input' type='password' value={smtpPass} autoComplete='new-password'
                placeholder={smtpConfigured ? '••••••••' : ''}
                onChange={(e) => setSmtpPass(e.target.value)} />
            </div>
            <div style={{ gridColumn: '1 / -1' }}>
              <label className='label'>{t('settings.smtp_from')}</label>
              <input className='input' type='text' value={smtpFrom} placeholder='alerts@example.com'
                onChange={(e) => setSmtpFrom(e.target.value)} />
            </div>
          </div>
          <div style={{ display: 'flex', gap: '0.75rem', marginTop: '1rem' }}>
            <button className='btn-primary' onClick={handleSaveSmtp} disabled={isSavingSmtp}>
              {isSavingSmtp ? t('settings.loading_plan') : t('common.save')}
            </button>
            <button className='btn-secondary' onClick={handleTestSmtp} disabled={isTestingSmtp || !smtpConfigured}>
              {isTestingSmtp ? t('settings.loading_plan') : t('settings.smtp_test')}
            </button>
          </div>
        </div>
      </section>

      {/* Team Section — listing org members is admin-only. */}
      {isAdmin && (
      <section className='settings-section'>
        <div className='section-header'>
          <h2>
            <HiUserGroup /> {t('settings.section_team')}
          </h2>
          <button className='btn-primary' onClick={() => setShowInviteModal(true)}>
            <HiPlus /> {t('settings.invite_user')}
          </button>
        </div>

        {isLoading ? (
          <div className='loading-state'>{t('settings.loading_users')}</div>
        ) : (
          <div className='users-list'>
            {users.map((member) => (
              <div key={member.id} className='user-card'>
                <div className='user-avatar-large'>
                  {member.name.charAt(0).toUpperCase()}
                </div>
                <div className='user-info'>
                  <h4>
                    {member.name}
                    {member.id === user?.id && (
                      <span className='badge badge-you'>{t('settings.you_badge')}</span>
                    )}
                  </h4>
                  <p>{member.email}</p>
                </div>
                <div className='user-role'>
                  {member.pending && (
                    <span className='badge badge-pending'>{t('settings.role_pending')}</span>
                  )}
                  {member.id === user?.id ? (
                    <span className={`badge badge-${member.role}`}>
                      {t(`settings.modal.role_${member.role}`, member.role)}
                    </span>
                  ) : (
                    <select
                      className={`role-select role-select-${member.role}`}
                      value={member.role}
                      onChange={(e) => handleChangeRole(member.id, e.target.value)}
                      aria-label={t('settings.modal.role')}
                    >
                      <option value='read_only'>{t('settings.modal.role_read_only')}</option>
                      <option value='read_write'>{t('settings.modal.role_read_write')}</option>
                      <option value='admin'>{t('settings.modal.role_admin')}</option>
                    </select>
                  )}
                </div>
                {member.id !== user?.id && (
                  <button
                    className='btn-icon danger'
                    onClick={() => setPendingDeleteUserId(member.id)}
                    title={t('settings.remove_user')}
                  >
                    <HiTrash />
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </section>
      )}

      {showInviteModal && (
        <InviteUserModal
          onClose={() => setShowInviteModal(false)}
          onSuccess={() => {
            setShowInviteModal(false);
            fetchUsers();
          }}
        />
      )}
      {pendingDeleteUserId !== null && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('settings.remove_user_confirm')}
          onConfirm={() => handleDeleteUser(pendingDeleteUserId)}
          onCancel={() => setPendingDeleteUserId(null)}
        />
      )}
      {showEditPlanModal && (
        <EditPlanModal
          onClose={() => setShowEditPlanModal(false)}
          onSuccess={() => {
            setShowEditPlanModal(false);
            fetchStats();
          }}
        />
      )}
    </div>
  );
}

interface PlanUsageBarProps {
  label: string;
  used: number;
  limit: number;
  unlimited: string;
  of: string;
  format?: (n: number) => string;
}

function PlanUsageBar({ label, used, limit, unlimited, of: ofLabel, format = String }: PlanUsageBarProps) {
  const isUnlimited = limit === -1;
  const pct = isUnlimited ? 0 : Math.min(100, (used / limit) * 100);
  const atLimit = !isUnlimited && used >= limit;

  return (
    <div className='usage-bar-row'>
      <div className='usage-bar-label'>
        <span>{label}</span>
        <span>{isUnlimited ? unlimited : `${format(used)} ${ofLabel} ${format(limit)}`}</span>
      </div>
      {!isUnlimited && (
        <div className='usage-bar-track'>
          <div
            className={`usage-bar-fill${atLimit ? ' at-limit' : ''}`}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </div>
  );
}

interface InviteUserModalProps {
  onClose: () => void;
  onSuccess: () => void;
}

function InviteUserModal({ onClose, onSuccess }: InviteUserModalProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [role, setRole] = useState('read_write');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [sent, setSent] = useState(false);

  const INVITE_EMAIL_RE = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;

  const validateInviteField = (field: string, value: string): string => {
    if (field === 'name' && !value.trim()) return t('settings.modal.error_name_required');
    if (field === 'email' && !INVITE_EMAIL_RE.test(value)) return t('settings.modal.error_email_invalid');
    return '';
  };

  const handleInviteBlur = (field: string, value: string) => {
    const err = validateInviteField(field, value);
    setFieldErrors(prev => ({ ...prev, [field]: err }));
  };

  const clearInviteFieldError = (field: string) => {
    if (fieldErrors[field]) setFieldErrors(prev => ({ ...prev, [field]: '' }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const nameErr = validateInviteField('name', name);
    const emailErr = validateInviteField('email', email);
    if (nameErr || emailErr) {
      setFieldErrors({ name: nameErr, email: emailErr });
      return;
    }
    setError('');
    setIsLoading(true);

    try {
      await orgApi.inviteUser({ name, email, role });
      setSent(true);
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.modal.error_invite'));
    } finally {
      setIsLoading(false);
    }
  };

  if (sent) {
    return (
      <div className='modal-overlay' onClick={onClose}>
        <div className='modal' onClick={(e) => e.stopPropagation()}>
          <div className='modal-header'>
            <h3>{t('settings.modal.success_title')}</h3>
            <button className='modal-close' onClick={onClose}>
              <HiXMark />
            </button>
          </div>
          <div className='modal-body'>
            <p className='success-message'>
              <HiCheck /> {t('settings.modal.success_message')}
            </p>
          </div>
          <div className='modal-footer'>
            <button className='btn-primary' onClick={onSuccess}>
              {t('common.done')}
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className='modal-overlay' onClick={onClose}>
      <div className='modal' onClick={(e) => e.stopPropagation()}>
        <div className='modal-header'>
          <h3>{t('settings.modal.invite_title')}</h3>
          <button className='modal-close' onClick={onClose}>
            <HiXMark />
          </button>
        </div>
        <form onSubmit={handleSubmit}>
          <div className='modal-body'>
            {error && (
              <div className='modal-error'>
                <HiExclamationCircle /> {error}
              </div>
            )}

            <div className='form-group'>
              <label>{t('settings.modal.full_name')}</label>
              <input
                type='text'
                value={name}
                onChange={(e) => { setName(e.target.value); clearInviteFieldError('name'); }}
                onBlur={(e) => handleInviteBlur('name', e.target.value)}
                placeholder={t('settings.modal.full_name_placeholder')}
                required
              />
              {fieldErrors.name && <p className='field-error'>{fieldErrors.name}</p>}
            </div>

            <div className='form-group'>
              <label>{t('settings.modal.email')}</label>
              <input
                type='email'
                value={email}
                onChange={(e) => { setEmail(e.target.value); clearInviteFieldError('email'); }}
                onBlur={(e) => handleInviteBlur('email', e.target.value)}
                placeholder={t('settings.modal.email_placeholder')}
                required
              />
              {fieldErrors.email && <p className='field-error'>{fieldErrors.email}</p>}
            </div>

            <div className='form-group'>
              <label>{t('settings.modal.role')}</label>
              <select value={role} onChange={(e) => setRole(e.target.value)}>
                <option value='read_only'>{t('settings.modal.role_read_only')}</option>
                <option value='read_write'>{t('settings.modal.role_read_write')}</option>
                <option value='admin'>{t('settings.modal.role_admin')}</option>
              </select>
              <p className='field-hint'>{t(`settings.modal.role_${role}_hint`)}</p>
            </div>
          </div>
          <div className='modal-footer'>
            <button type='button' className='btn-secondary' onClick={onClose}>
              {t('common.cancel')}
            </button>
            <button type='submit' className='btn-primary' disabled={isLoading}>
              {isLoading ? t('settings.modal.sending') : t('settings.modal.send_invitation')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

interface EditPlanModalProps {
  onClose: () => void;
  onSuccess: () => void;
}

function EditPlanModal({ onClose, onSuccess }: EditPlanModalProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { refreshUser } = useAuth();
  const [values, setValues] = useState<CustomPlanValues | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState('');

  const handleSave = async () => {
    if (!values) return;
    setIsSaving(true);
    setError('');
    try {
      await orgApi.payments.updateSubscription({
        hosts: values.hosts,
        services: values.services,
        sdk_services: values.sdk_services,
        retention_days: values.retention_days,
      });
      await refreshUser();
      onSuccess();
    } catch (err: any) {
      setError(err.response?.data?.error || t('settings.error_update_org'));
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <div className='modal-overlay' onClick={onClose}>
      <div className='modal' onClick={(e) => e.stopPropagation()}>
        <div className='modal-header'>
          <h3>{t('settings.adjust_plan')}</h3>
          <button className='modal-close' onClick={onClose}>
            <HiXMark />
          </button>
        </div>
        <div className='modal-body'>
          {error && (
            <div className='modal-error'>
              <HiExclamationCircle /> {error}
            </div>
          )}
          <CustomPlanCalculator onValuesChange={setValues} />
          <p className='text-sm' style={{ color: 'var(--text-secondary)', marginTop: '1rem' }}>
            {t('pricing.update_modal_note')}
          </p>
        </div>
        <div className='modal-footer'>
          <button type='button' className='btn-secondary' onClick={onClose} disabled={isSaving}>
            {t('common.cancel')}
          </button>
          <button type='button' className='btn-primary' onClick={handleSave} disabled={isSaving || !values}>
            {isSaving ? t('pricing.updating') : t('pricing.update_modal_confirm')}
          </button>
        </div>
      </div>
    </div>
  );
}
