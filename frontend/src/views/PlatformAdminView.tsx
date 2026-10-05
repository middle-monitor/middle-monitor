import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiOutlineBuildingOffice2, HiOutlineLockClosed, HiOutlineArrowLeft } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { platformAdminApi, type PlatformOrganization } from '../api';

const PLANS = ['free', 'pro', 'custom'] as const;

export default function PlatformAdminView() {
  const { t, i18n } = useTranslation();
  const { isPlatformAdmin, organization } = useAuth();
  const [orgs, setOrgs] = useState<PlatformOrganization[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    if (!isPlatformAdmin) {
      setIsLoading(false);
      return;
    }
    platformAdminApi
      .listOrganizations()
      .then((res) => setOrgs(res.data))
      // The backend refuses this route for an unverified address or a missing
      // authenticator too, and that reason is worth more than "loading failed".
      .catch((err) => setError(err.response?.data?.error || t('platform_admin.error_load')))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isPlatformAdmin]);

  const handleChangePlan = async (orgId: number, plan: string) => {
    const previous = orgs.find((o) => o.id === orgId);
    // Optimistic, and scoped to the row: a whole-list snapshot would undo a
    // concurrent change to another row on rollback. The write also closes the
    // trial server-side, so the deadline has to go with it — leaving it on
    // screen is the trial/subscription mix-up the column exists to prevent.
    setOrgs((list) =>
      list.map((o) => (o.id === orgId ? { ...o, plan, trial_ends_at: undefined } : o))
    );
    setError('');
    setSuccess('');
    try {
      await platformAdminApi.setPlan(orgId, plan);
      setSuccess(t('platform_admin.plan_updated'));
    } catch (err: any) {
      // The whole row goes back: the refused write left the trial in place too.
      setOrgs((list) => list.map((o) => (o.id === orgId && previous ? previous : o)));
      setError(err.response?.data?.error || t('platform_admin.error_update_plan'));
    }
  };

  // No sidebar out here, so every state has to carry its own way back: a stale
  // link would otherwise leave the browser's back button as the only exit.
  const backLink = (className: string) =>
    organization && (
      <Link className={className} to={`/organizations/${organization.slug}`}>
        <HiOutlineArrowLeft /> {t('platform_admin.back')}
      </Link>
    );

  if (!isPlatformAdmin) {
    return (
      <div className='empty-state'>
        <HiOutlineLockClosed className='empty-state-icon' />
        <div className='empty-state-title'>{t('platform_admin.forbidden_title')}</div>
        <div className='empty-state-description'>{t('platform_admin.forbidden_desc')}</div>
        {backLink('empty-state-action')}
      </div>
    );
  }

  return (
    <div style={{ padding: '2rem', maxWidth: '1100px', margin: '0 auto' }}>
      <div className='page-header'>
        <div>
          <h1 className='page-title'>{t('platform_admin.title')}</h1>
          <p className='page-subtitle'>{t('platform_admin.subtitle')}</p>
        </div>
        {backLink('btn-secondary')}
      </div>

      {error && <div className='error-message'>{error}</div>}
      {success && <div className='success-message'>{success}</div>}

      <div className='card'>
        <div className='card-title'>
          <HiOutlineBuildingOffice2 className='card-title-icon' />
          {t('platform_admin.title')} ({orgs.length})
        </div>

        {isLoading ? (
          <div className='loading-state'>{t('platform_admin.loading')}</div>
        ) : orgs.length === 0 ? (
          <div className='empty-state'>
            <HiOutlineBuildingOffice2 className='empty-state-icon' />
            <div className='empty-state-title'>{t('platform_admin.empty_title')}</div>
          </div>
        ) : (
          <table className='table'>
            <thead>
              <tr>
                <th>{t('platform_admin.col_organization')}</th>
                <th>{t('platform_admin.col_slug')}</th>
                <th>{t('platform_admin.col_plan')}</th>
                <th>{t('platform_admin.col_trial')}</th>
                <th>{t('platform_admin.col_created')}</th>
              </tr>
            </thead>
            <tbody>
              {orgs.map((org) => (
                <tr key={org.id}>
                  <td>{org.name}</td>
                  <td>{org.slug}</td>
                  <td>
                    <select
                      className='input'
                      value={org.plan}
                      onChange={(e) => handleChangePlan(org.id, e.target.value)}
                      style={{ width: 'auto', minWidth: '120px', fontSize: '0.8125rem', padding: '0.25rem 0.5rem' }}
                    >
                      {PLANS.map((plan) => (
                        <option key={plan} value={plan}>
                          {t(`platform_admin.plan_${plan}`)}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {org.trial_ends_at
                      ? t('platform_admin.trial_ends', {
                          date: new Date(org.trial_ends_at).toLocaleDateString(i18n.language),
                        })
                      : '—'}
                  </td>
                  <td>{new Date(org.created_at).toLocaleDateString(i18n.language)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
