import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  HiOutlineArrowTopRightOnSquare,
  HiOutlineClipboardDocument,
  HiOutlineMegaphone,
} from 'react-icons/hi2';

import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useAuth } from '../contexts/AuthContext';
import { Skeleton } from '../components/Skeleton';
import { apiErrorMessage } from '../utils/apiError';

function StatusPageSettingsView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const queryClient = useQueryClient();
  const { isAdmin, organization } = useAuth();
  const { orgSlug } = useParams<{ orgSlug: string }>();
  const slug = orgSlug || organization?.slug || '';

  const queryKey = [scope, 'status-page'];
  const checksQuery = useQuery({
    queryKey,
    queryFn: async () => (await orgApi.statusPage.list()).data,
  });
  const checks = checksQuery.data ?? [];
  const savedIds = checks.filter((c) => c.public).map((c) => c.id);

  // null until the admin ticks something: the saved selection is the default.
  const [selection, setSelection] = useState<Set<number> | null>(null);
  const selected = selection ?? new Set(savedIds);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const url = `${window.location.origin}/status/${slug}`;
  const isPublished = savedIds.length > 0;

  const toggle = (id: number) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelection(next);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const res = await orgApi.statusPage.update([...selected]);
      queryClient.setQueryData(queryKey, res.data);
      setSelection(null);
    } catch (err) {
      setError(apiErrorMessage(err, t('status_page_settings.error_save')));
    } finally {
      setSaving(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard denied: the URL stays selectable */
    }
  };

  return (
    <div>
      <div className='page-header'>
        <div>
          <h1 className='page-title'>{t('status_page_settings.title')}</h1>
          <p className='page-subtitle'>{t('status_page_settings.desc')}</p>
        </div>
      </div>

      <div className='card' style={{ marginBottom: '1.5rem' }}>
        <div className='card-title'>
          <HiOutlineMegaphone className='card-title-icon' />
          {t('status_page_settings.public_url')}
        </div>
        {isPublished ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexWrap: 'wrap', marginTop: '0.75rem' }}>
            <code style={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere', fontSize: '0.8125rem' }}>{url}</code>
            <button type='button' className='btn btn-secondary' onClick={copy} style={{ display: 'inline-flex', alignItems: 'center', gap: '0.375rem' }}>
              <HiOutlineClipboardDocument />
              {copied ? t('common.copied') : t('status_page_settings.copy')}
            </button>
            <a href={url} target='_blank' rel='noopener noreferrer' className='btn btn-secondary' style={{ display: 'inline-flex', alignItems: 'center', gap: '0.375rem' }}>
              <HiOutlineArrowTopRightOnSquare />
              {t('status_page_settings.open')}
            </a>
          </div>
        ) : (
          <p style={{ color: 'var(--text-secondary)', fontSize: '0.875rem', margin: '0.75rem 0 0' }}>
            {t('status_page_settings.not_published')}
          </p>
        )}
      </div>

      <div className='card'>
        <div className='card-title'>{t('status_page_settings.checks_title')}</div>
        <p style={{ color: 'var(--text-secondary)', fontSize: '0.8125rem', margin: '0.25rem 0 1rem' }}>
          {t('status_page_settings.checks_hint')}
        </p>

        {error && (
          <div className='error-message' style={{ marginBottom: '1rem' }}>
            {error}
          </div>
        )}

        {checksQuery.isPending ? (
          <Skeleton rows={4} />
        ) : checks.length === 0 ? (
          <p style={{ color: 'var(--text-secondary)', fontSize: '0.875rem' }}>
            {t('status_page_settings.no_checks')}
          </p>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
            {checks.map((check) => (
              <li key={check.id} style={{ padding: '0.5rem 0', borderBottom: '1px solid var(--border-primary)' }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', cursor: isAdmin ? 'pointer' : 'default' }}>
                  <input
                    type='checkbox'
                    checked={selected.has(check.id)}
                    onChange={() => toggle(check.id)}
                    disabled={!isAdmin}
                  />
                  <span style={{ fontWeight: 500 }}>{check.display_name || check.name}</span>
                  <span className='status-badge status-info' style={{ fontSize: '0.7rem' }}>
                    {check.type.toUpperCase()}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}

        {isAdmin ? (
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: '1rem' }}>
            <button type='button' className='btn btn-primary' onClick={save} disabled={saving || selection === null}>
              {saving ? t('status_page_settings.saving') : t('status_page_settings.save')}
            </button>
          </div>
        ) : (
          <p style={{ color: 'var(--text-tertiary)', fontSize: '0.8125rem', marginTop: '1rem' }}>
            {t('status_page_settings.admin_only')}
          </p>
        )}
      </div>
    </div>
  );
}

export default StatusPageSettingsView;
