import { useState, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import {
  HiShieldCheck,
  HiCheck,
  HiExclamationCircle,
  HiXMark,
  HiOutlineKey,
  HiOutlinePlus,
  HiOutlineTrash,
  HiOutlineClipboard,
  HiOutlineCheckCircle,
  HiOutlineExclamationTriangle,
} from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { authApi, APIKey } from '../api';
import { TotpEnrollment } from '../components/TotpEnrollment';
import { isDemoMode } from '../demo/demoMode';
import { ConfirmDialog } from '../components/ConfirmDialog';
import './SettingsView.css';
import './APIKeysView.css';

/**
 * Personal account settings: each user manages their own two-factor authentication
 * and their personal API tokens (which authenticate as them). Org-wide settings
 * (enforcement, org/agent tokens, billing, team) live in the Organisation page.
 */
export default function AccountView() {
  const { t, i18n } = useTranslation();
  const { user, organization, refreshUser } = useAuth();
  const [error, setError] = useState('');

  // --- Two-factor ---
  const [enrolling, setEnrolling] = useState(false);
  const [disabling, setDisabling] = useState(false);
  const totpEnabled = !!user?.totp_enabled;
  const orgRequiresMfa = !!organization?.mfa_required;

  const handleEnrolled = async () => {
    setEnrolling(false);
    await refreshUser();
  };

  const handleDisable = async () => {
    setError('');
    setDisabling(true);
    try {
      await authApi.disable2fa();
      await refreshUser();
    } catch (err: any) {
      setError(err.response?.data?.error || t('account.mfa.disable_error'));
    } finally {
      setDisabling(false);
    }
  };

  // --- Personal API tokens ---
  const [tokens, setTokens] = useState<APIKey[]>([]);
  const [loadingTokens, setLoadingTokens] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [form, setForm] = useState({ name: '', expires_at: '' });
  const [pendingDelete, setPendingDelete] = useState<number | null>(null);

  const fetchTokens = useCallback(async () => {
    // Personal tokens are not org-scoped, so they never went through the demo
    // client: the call reached the backend, answered 401 and logged the visitor
    // out of the demo. The demo viewer owns no token anyway.
    if (isDemoMode()) {
      setTokens([]);
      setLoadingTokens(false);
      return;
    }
    setLoadingTokens(true);
    try {
      const res = await authApi.personalTokens.list();
      setTokens(res.data || []);
    } catch {
      /* listing failure is non-fatal; the section just shows empty */
    } finally {
      setLoadingTokens(false);
    }
  }, []);

  useEffect(() => {
    fetchTokens();
  }, [fetchTokens]);

  const handleCreateToken = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.expires_at) return;
    setError('');
    try {
      const res = await authApi.personalTokens.create({
        name: form.name,
        // End-of-day for the chosen date.
        expires_at: new Date(`${form.expires_at}T23:59:59`).toISOString(),
      });
      setNewToken(res.data.key);
      setForm({ name: '', expires_at: '' });
      setShowForm(false);
      fetchTokens();
    } catch (err: any) {
      setError(err.response?.data?.error || t('account.tokens.create_error'));
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className='settings-view'>
      <header className='view-header settings-header'>
        <div>
          <h1>{t('account.title')}</h1>
          <p className='subtitle'>{t('account.subtitle')}</p>
        </div>
      </header>

      {error && (
        <div className='settings-error'>
          <HiExclamationCircle />
          <span>{error}</span>
          <button onClick={() => setError('')}>
            <HiXMark />
          </button>
        </div>
      )}

      {/* Two-factor authentication */}
      <section className='settings-section'>
        <div className='section-header'>
          <h2><HiShieldCheck /> {t('account.mfa.title')}</h2>
        </div>
        <div className='org-card'>
          <p className='text-sm' style={{ color: 'var(--text-secondary)', margin: '0 0 1rem' }}>
            {t('account.mfa.desc')}
          </p>

          {totpEnabled ? (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '1rem' }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', color: 'var(--status-success)', fontWeight: 600 }}>
                <HiCheck /> {t('account.mfa.enabled')}
              </span>
              {orgRequiresMfa ? (
                <span className='text-sm' style={{ color: 'var(--text-tertiary)' }}>
                  {t('account.mfa.org_enforced')}
                </span>
              ) : (
                <button className='btn-secondary' onClick={handleDisable} disabled={disabling}>
                  {t('account.mfa.disable')}
                </button>
              )}
            </div>
          ) : enrolling ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', alignItems: 'flex-start', maxWidth: 420 }}>
              <TotpEnrollment onEnrolled={handleEnrolled} onCancel={() => setEnrolling(false)} />
            </div>
          ) : (
            <button className='btn-primary' onClick={() => setEnrolling(true)}>
              {t('account.mfa.enable')}
            </button>
          )}
        </div>
      </section>

      {/* Personal API tokens */}
      <section className='settings-section'>
        <div className='section-header'>
          <h2><HiOutlineKey /> {t('account.tokens.title')}</h2>
          <button className='btn-primary' onClick={() => { setShowForm(true); setNewToken(null); }}>
            <HiOutlinePlus /> {t('account.tokens.new')}
          </button>
        </div>
        <div className='org-card'>
          <p className='text-sm' style={{ color: 'var(--text-secondary)', margin: '0 0 1rem' }}>
            {t('account.tokens.desc')}
          </p>

          {newToken && (
            <div className='api-key-alert'>
              <div className='api-key-alert-header'>
                <HiOutlineExclamationTriangle />
                <strong>{t('api_keys.created_success')}</strong>
              </div>
              <p>{t('api_keys.copy_now')}</p>
              <div className='api-key-secret'>
                <code>{newToken}</code>
                <button onClick={() => copyToClipboard(newToken)} className='btn-copy'>
                  {copied ? <><HiOutlineCheckCircle /> {t('common.copied')}</> : <><HiOutlineClipboard /> {t('common.copy')}</>}
                </button>
              </div>
              <button className='btn-dismiss' onClick={() => setNewToken(null)}>{t('common.close')}</button>
            </div>
          )}

          {showForm && !newToken && (
            <form onSubmit={handleCreateToken} className='alert-form' style={{ marginBottom: '1rem' }}>
              <div className='form-row'>
                <label>{t('account.tokens.name')}</label>
                <input
                  type='text'
                  required
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder={t('account.tokens.name_placeholder')}
                />
              </div>
              <div className='form-row'>
                <label>{t('api_keys.form.expires_at')}</label>
                <input
                  type='date'
                  required
                  min={new Date(Date.now() + 86400000).toISOString().slice(0, 10)}
                  value={form.expires_at}
                  onChange={(e) => setForm({ ...form, expires_at: e.target.value })}
                />
              </div>
              <div className='form-actions'>
                <button type='button' className='btn-secondary' onClick={() => setShowForm(false)}>{t('common.cancel')}</button>
                <button type='submit' className='btn-primary'>{t('account.tokens.create_button')}</button>
              </div>
            </form>
          )}

          {loadingTokens ? (
            <div className='api-keys-loading'>{t('common.loading')}</div>
          ) : tokens.length === 0 ? (
            <div className='empty-state'>
              <HiOutlineKey className='empty-state-icon' />
              <div className='empty-state-title'>{t('account.tokens.empty')}</div>
            </div>
          ) : (
            <table className='api-keys-table'>
              <thead>
                <tr>
                  <th>{t('api_keys.table.name')}</th>
                  <th>{t('api_keys.table.key')}</th>
                  <th>{t('api_keys.table.last_used')}</th>
                  <th>{t('api_keys.table.created')}</th>
                  <th>{t('common.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((tok) => (
                  <tr key={tok.id}>
                    <td className='key-name'>{tok.name}</td>
                    <td><code className='key-prefix'>{tok.key_prefix}&bull;&bull;&bull;&bull;&bull;&bull;&bull;</code></td>
                    <td className='key-date'>{tok.last_used_at ? new Date(tok.last_used_at).toLocaleString(i18n.language) : t('common.never')}</td>
                    <td className='key-date'>{new Date(tok.created_at).toLocaleDateString(i18n.language)}</td>
                    <td>
                      <button className='btn-icon-sm danger' onClick={() => setPendingDelete(tok.id)} title={t('common.delete')}>
                        <HiOutlineTrash />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      {pendingDelete !== null && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('account.tokens.delete_confirm')}
          onConfirm={async () => {
            try {
              await authApi.personalTokens.delete(pendingDelete);
              fetchTokens();
            } finally {
              setPendingDelete(null);
            }
          }}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}
