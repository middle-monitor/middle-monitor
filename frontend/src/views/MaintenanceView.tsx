import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  HiOutlineWrenchScrewdriver,
  HiOutlinePlus,
  HiOutlineTrash,
  HiOutlineServerStack,
  HiOutlineCube,
} from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { usePlan, ProUpgradeBanner } from '../components/PlanGate';
import type { MaintenanceWindow, Host, ServiceWithResults } from '../api';
import { ConfirmDialog } from '../components/ConfirmDialog';

type TargetType = 'service' | 'host';

function toLocalInput(d: Date): string {
  // datetime-local wants "YYYY-MM-DDTHH:mm" in local time.
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function windowState(w: MaintenanceWindow): 'active' | 'scheduled' | 'past' {
  const now = Date.now();
  const start = new Date(w.starts_at).getTime();
  const end = new Date(w.ends_at).getTime();
  if (now >= start && now <= end) return 'active';
  if (now < start) return 'scheduled';
  return 'past';
}

export default function MaintenanceView() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const { canWrite } = useAuth();
  const { isPro } = usePlan();
  const { orgPath } = useOrgPath();

  const [windows, setWindows] = useState<MaintenanceWindow[]>([]);
  const [hosts, setHosts] = useState<Host[]>([]);
  const [services, setServices] = useState<ServiceWithResults[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showForm, setShowForm] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [pendingDeleteId, setPendingDeleteId] = useState<number | null>(null);

  const now = new Date();
  const inTwoHours = new Date(now.getTime() + 2 * 60 * 60 * 1000);
  const [form, setForm] = useState({
    name: '',
    target_type: 'service' as TargetType,
    target_id: '' as string,
    starts_at: toLocalInput(now),
    ends_at: toLocalInput(inTwoHours),
  });

  const load = useCallback(async () => {
    try {
      const [w, h, s] = await Promise.all([
        orgApi.maintenance.list(),
        orgApi.hosts.list(),
        orgApi.services.list(),
      ]);
      setWindows(w.data || []);
      setHosts(h.data || []);
      setServices(s.data || []);
    } catch {
      setError(t('maintenance.error_load'));
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    if (!form.name.trim() || !form.target_id) {
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
        target_type: form.target_type,
        target_id: Number(form.target_id),
        starts_at: start.toISOString(),
        ends_at: end.toISOString(),
      });
      setShowForm(false);
      setForm({ ...form, name: '', target_id: '' });
      await load();
    } catch (err: any) {
      setError(err.response?.data?.error || t('maintenance.error_create'));
    } finally {
      setSubmitting(false);
    }
  };

  const doDelete = async (id: number) => {
    try {
      await orgApi.maintenance.delete(id);
      setWindows((prev) => prev.filter((w) => w.id !== id));
    } catch {
      setError(t('maintenance.error_delete'));
    } finally {
      setPendingDeleteId(null);
    }
  };

  const targetOptions =
    form.target_type === 'host'
      ? hosts.map((h) => ({ id: h.id, label: h.display_name || h.name }))
      : services
          .filter((s) => !s.type?.startsWith('error_service'))
          .map((s) => ({
            id: s.id,
            label: s.host_name ? `${s.host_name} / ${s.name}` : s.name,
          }));

  const fmt = (iso: string) =>
    new Date(iso).toLocaleString(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    });

  const stateBadge = (s: 'active' | 'scheduled' | 'past') => {
    const map = {
      active: { bg: 'var(--status-success-bg)', color: 'var(--status-success)' },
      scheduled: { bg: 'var(--status-info-bg)', color: 'var(--status-info)' },
      past: { bg: 'var(--bg-active)', color: 'var(--text-tertiary)' },
    }[s];
    return (
      <span style={{ background: map.bg, color: map.color, padding: '0.15rem 0.6rem', borderRadius: 999, fontSize: '0.7rem', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.03em' }}>
        {t(`maintenance.state_${s}`)}
      </span>
    );
  };

  return (
    <div>
      <header className="view-header" style={{ marginBottom: '1.5rem' }}>
        <div>
          <h1>
            <HiOutlineWrenchScrewdriver style={{ verticalAlign: '-3px', marginRight: '0.4rem' }} />
            {t('maintenance.title')}
          </h1>
          <p className="subtitle">{t('maintenance.subtitle')}</p>
        </div>
        {canWrite && (
          <button className="btn btn-primary" onClick={() => setShowForm((v) => !v)} disabled={!isPro} style={{ display: 'inline-flex', alignItems: 'center', gap: '0.4rem' }}>
            <HiOutlinePlus /> {t('maintenance.schedule')}
          </button>
        )}
      </header>

      {!isPro && (
        <div style={{ marginBottom: '1.5rem' }}>
          <ProUpgradeBanner feature={t('maintenance.title')} />
        </div>
      )}

      {error && (
        <div className="settings-error" style={{ marginBottom: '1rem' }}>{error}</div>
      )}

      {showForm && (
        <form onSubmit={handleCreate} className="card" style={{ marginBottom: '1.5rem', display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div className="form-row">
            <label>{t('maintenance.field_name')}</label>
            <input className="form-control" value={form.name} placeholder={t('maintenance.name_placeholder')}
              onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div className="form-row-group">
            <div className="form-row">
              <label>{t('maintenance.field_target_type')}</label>
              <select className="form-control" value={form.target_type}
                onChange={(e) => setForm({ ...form, target_type: e.target.value as TargetType, target_id: '' })}>
                <option value="service">{t('maintenance.target_service')}</option>
                <option value="host">{t('maintenance.target_host')}</option>
              </select>
            </div>
            <div className="form-row">
              <label>{t('maintenance.field_target')}</label>
              <select className="form-control" value={form.target_id}
                onChange={(e) => setForm({ ...form, target_id: e.target.value })}>
                <option value="">{t('maintenance.select_target')}</option>
                {targetOptions.map((o) => (
                  <option key={o.id} value={o.id}>{o.label}</option>
                ))}
              </select>
            </div>
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
          <div className="form-actions">
            <button type="button" className="btn btn-secondary" onClick={() => setShowForm(false)}>{t('common.cancel')}</button>
            <button type="submit" className="btn btn-primary" disabled={submitting}>
              {submitting ? t('common.loading') : t('maintenance.create')}
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <p style={{ color: 'var(--text-secondary)' }}>{t('common.loading')}</p>
      ) : windows.length === 0 ? (
        <div className="empty-state">
          <HiOutlineWrenchScrewdriver className="empty-state-icon" />
          <div className="empty-state-title">{t('maintenance.empty')}</div>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          {windows.map((w) => {
            const st = windowState(w);
            return (
              <div key={w.id} className="card" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '1rem', opacity: st === 'past' ? 0.6 : 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', minWidth: 0 }}>
                  {w.target_type === 'host' ? <HiOutlineServerStack /> : <HiOutlineCube />}
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 600, display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                      {w.name} {stateBadge(st)}
                    </div>
                    <div style={{ fontSize: '0.8rem', color: 'var(--text-secondary)' }}>
                      {t(`maintenance.target_${w.target_type}`)}: {' '}
                      <Link
                        to={orgPath(`/${w.target_type === 'host' ? 'hosts' : 'services'}/${w.target_id}`)}
                        style={{ color: 'var(--brand-primary)', textDecoration: 'none', fontWeight: 500 }}
                        onClick={(e) => e.stopPropagation()}
                      >
                        {w.target_name || `#${w.target_id}`}
                      </Link>
                      {' · '}{fmt(w.starts_at)} → {fmt(w.ends_at)}
                    </div>
                  </div>
                </div>
                {canWrite && (
                  <button className="btn-icon" title={t('common.delete')} onClick={() => setPendingDeleteId(w.id)} style={{ color: 'var(--status-error)' }}>
                    <HiOutlineTrash />
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}
      {pendingDeleteId !== null && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('maintenance.delete_confirm')}
          onConfirm={() => doDelete(pendingDeleteId)}
          onCancel={() => setPendingDeleteId(null)}
        />
      )}
    </div>
  );
}
