import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  HiOutlineKey,
  HiOutlinePlus,
  HiOutlineTrash,
  HiOutlineClipboard,
  HiOutlineXMark,
  HiOutlineCheckCircle,
  HiOutlineExclamationTriangle,
  HiOutlineServerStack,
} from 'react-icons/hi2';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { PUBLIC_API_URL as API_URL, type APIKey, type InstallToken } from '../api';
import { ConfirmDialog } from '../components/ConfirmDialog';
import './APIKeysView.css';

function APIKeysView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const [showForm, setShowForm] = useState(false);
  const [newKey, setNewKey] = useState<string | null>(null);
  const [newInstallToken, setNewInstallToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [formData, setFormData] = useState({ name: '', expires_at: '' });
  const [pendingAction, setPendingAction] = useState<{ message: string; confirmLabel?: string; onConfirm: () => void } | null>(null);

  // An open form or a pending confirmation is a detail row: refreshing under
  // one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'api-keys',
    0,
    showForm || pendingAction !== null,
  );

  const keysQueryKey = [scope, 'api-keys', 'list'];
  const keysQuery = useQuery({
    queryKey: keysQueryKey,
    queryFn: async () => {
      const [keysRes, tokensRes] = await Promise.all([
        orgApi.apiKeys.list(),
        orgApi.installTokens.list(),
      ]);
      return {
        keys: (keysRes.data as APIKey[]) || [],
        installTokens: (tokensRes.data as InstallToken[]) || [],
      };
    },
    refetchInterval,
  });

  const keys = keysQuery.data?.keys ?? [];
  const installTokens = keysQuery.data?.installTokens ?? [];

  const autoRefresh = buildControl({
    query: keysQuery,
    queryKey: keysQueryKey,
    prefix: [scope, 'api-keys'],
  });
  const { refresh } = autoRefresh;

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!formData.expires_at) return; // expiration is mandatory
    try {
      const res = await orgApi.apiKeys.create({
        name: formData.name,
        // Send end-of-day for the chosen date.
        expires_at: new Date(`${formData.expires_at}T23:59:59`).toISOString(),
      });
      setNewKey(res.data.key);
      setFormData({ name: '', expires_at: '' });
      refresh();
    } catch (err) {
      console.error('Failed to create API key:', err);
    }
  };

  const handleDelete = async (id: number) => {
    setPendingAction({
      message: t('api_keys.delete_confirm'),
      onConfirm: async () => {
        try {
          await orgApi.apiKeys.delete(id);
          refresh();
        } catch (err) {
          console.error('Failed to delete API key:', err);
        } finally {
          setPendingAction(null);
        }
      },
    });
  };

  const handleCreateInstallToken = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const res = await orgApi.installTokens.create({});
      setNewInstallToken(res.data.token);
      refresh();
    } catch (err) {
      console.error('Failed to create install token:', err);
    }
  };

  const handleDeleteInstallToken = async (id: number) => {
    setPendingAction({
      message: t('api_keys.revoke_confirm'),
      confirmLabel: t('common.confirm'),
      onConfirm: async () => {
        try {
          await orgApi.installTokens.delete(id);
          refresh();
        } catch (err) {
          console.error('Failed to delete install token:', err);
        } finally {
          setPendingAction(null);
        }
      },
    });
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="api-keys-view">
      <header className="view-header">
        <div>
          <h1><HiOutlineKey /> {t('api_keys.title')}</h1>
          <p className="subtitle">{t('api_keys.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button className="btn-primary" onClick={() => { setShowForm(true); setNewKey(null); }}>
              <HiOutlinePlus /> {t('api_keys.new_key')}
            </button>
          )}
        </div>
      </header>

      {/* New key alert */}
      {newKey && (
        <div className="api-key-alert">
          <div className="api-key-alert-header">
            <HiOutlineExclamationTriangle />
            <strong>{t('api_keys.created_success')}</strong>
          </div>
          <p>{t('api_keys.copy_now')}</p>
          <div className="api-key-secret">
            <code>{newKey}</code>
            <button onClick={() => copyToClipboard(newKey)} className="btn-copy">
              {copied ? <><HiOutlineCheckCircle /> {t('common.copied')}</> : <><HiOutlineClipboard /> {t('common.copy')}</>}
            </button>
          </div>
          <button className="btn-dismiss" onClick={() => setNewKey(null)}>{t('api_keys.copied')}</button>
        </div>
      )}

      {/* Create form modal */}
      {showForm && !newKey && (
        <div className="alert-modal-overlay" onClick={() => setShowForm(false)}>
          <div className="alert-modal" onClick={e => e.stopPropagation()}>
            <div className="alert-modal-header">
              <h3>{t('api_keys.form.create_title')}</h3>
              <button className="btn-close" onClick={() => setShowForm(false)}><HiOutlineXMark /></button>
            </div>
            <form onSubmit={handleCreate} className="alert-form">
              <p className="form-hint">{t('api_keys.form.access_note')}</p>
              <div className="form-row">
                <label>{t('api_keys.form.name')}</label>
                <input type="text" required value={formData.name} onChange={e => setFormData({ ...formData, name: e.target.value })} placeholder={t('api_keys.form.name_placeholder')} />
              </div>
              <div className="form-row">
                <label>{t('api_keys.form.expires_at')}</label>
                <input
                  type="date"
                  required
                  min={new Date(Date.now() + 86400000).toISOString().slice(0, 10)}
                  value={formData.expires_at}
                  onChange={e => setFormData({ ...formData, expires_at: e.target.value })}
                />
                <span className="form-hint">{t('api_keys.form.expires_hint')}</span>
              </div>
              <div className="form-actions">
                <button type="button" className="btn-secondary" onClick={() => setShowForm(false)}>{t('common.cancel')}</button>
                <button type="submit" className="btn-primary">{t('api_keys.form.create_button')}</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Keys List */}
      <div className="api-keys-list">
        {keysQuery.isPending ? (
          <div className="api-keys-loading">{t('common.loading')}</div>
        ) : keys.length === 0 ? (
          <div className="empty-state">
            <HiOutlineKey className="empty-state-icon" />
            <div className="empty-state-title">{t('api_keys.empty_title')}</div>
            <div className="empty-state-description">{t('api_keys.empty_desc_sdk')}</div>
          </div>
        ) : (
          <table className="api-keys-table">
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
              {keys.map(key => (
                <tr key={key.id}>
                  <td className="key-name">{key.name}</td>
                  <td><code className="key-prefix">{key.key_prefix}•••••••</code></td>
                  <td className="key-date">{key.last_used_at ? new Date(key.last_used_at).toLocaleString(i18n.language) : t('common.never')}</td>
                  <td className="key-date">{new Date(key.created_at).toLocaleDateString(i18n.language)}</td>
                  <td>
                    {canWrite && (
                      <button className="btn-icon-sm danger" onClick={() => handleDelete(key.id)} title={t('common.delete')}>
                        <HiOutlineTrash />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Install Tokens Section */}
      <div className="api-keys-section install-tokens-section">
        <header className="view-header">
          <div>
            <h2><HiOutlineServerStack /> {t('api_keys.install_tokens.title')}</h2>
            <p className="subtitle">{t('api_keys.install_tokens.subtitle')}</p>
          </div>
          {canWrite && (
            <button
              className="btn-primary"
              onClick={handleCreateInstallToken}
              disabled={!!newInstallToken}
            >
              <HiOutlinePlus /> {t('api_keys.install_tokens.generate')}
            </button>
          )}
        </header>

        {newInstallToken && (
          <div className="api-key-alert">
            <div className="api-key-alert-header">
              <HiOutlineExclamationTriangle />
              <strong>{t('api_keys.install_tokens.created')}</strong>
            </div>
            <p>{t('api_keys.install_tokens.copy_instructions')}</p>
            <p className="install-prefer-env">{t('api_keys.install_tokens.prefer_env')}</p>
            <pre className="install-curl">
              {`curl -fsSL "${API_URL}/api/v1/agents/download/install" | MIDDLE_MONITOR_INSTALL_TOKEN=${newInstallToken} bash`}
            </pre>
            <p className="install-alt">{t('api_keys.install_tokens.or_url')}</p>
            <pre className="install-curl install-curl-alt">
              {`curl -fsSL "${API_URL}/api/v1/agents/download/install?token=${newInstallToken}" | bash`}
            </pre>
            <div className="install-curl-actions">
              <button onClick={() => copyToClipboard(`curl -fsSL "${API_URL}/api/v1/agents/download/install" | MIDDLE_MONITOR_INSTALL_TOKEN=${newInstallToken} bash`)} className="btn-copy">
                {copied ? <><HiOutlineCheckCircle /> {t('common.copied')}</> : <><HiOutlineClipboard /> {t('api_keys.install_tokens.copy_env')}</>}
              </button>
              <button className="btn-dismiss" onClick={() => setNewInstallToken(null)}>{t('common.close')}</button>
            </div>
          </div>
        )}

        {installTokens.length > 0 && (
          <table className="api-keys-table">
            <thead>
              <tr>
                <th>{t('api_keys.table.prefix')}</th>
                <th>{t('api_keys.table.created')}</th>
                <th>{t('common.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {installTokens.map(token => (
                <tr key={token.id}>
                  <td><code className="key-prefix">{token.token_prefix || token.token?.slice(0, 8) || '••••••••'}•••••••</code></td>
                  <td className="key-date">{new Date(token.created_at).toLocaleDateString(i18n.language)}</td>
                  <td>
                    {canWrite && (
                      <button className="btn-icon-sm danger" onClick={() => handleDeleteInstallToken(token.id)} title={t('common.revoke')}>
                        <HiOutlineTrash />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {pendingAction && (
        <ConfirmDialog
          title={t('common.confirm')}
          message={pendingAction.message}
          confirmLabel={pendingAction.confirmLabel}
          onConfirm={pendingAction.onConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}
    </div>
  );
}

export default APIKeysView;
