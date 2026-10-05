import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { type Host, type HostGroup } from '../../../api';
import { useOrgApi } from '../../../hooks/useOrgApi';
import { slugifyName } from '../../../utils/slugify';
import { PlanLimitNotice } from '../../../components/PlanGate';

interface AddHostFormProps {
  host?: Host | null;
  onSuccess: () => void;
  onCancel: () => void;
}

const NAME_RE = /^[a-zA-Z0-9._-]+$/;

export function AddHostForm({ host, onSuccess, onCancel }: AddHostFormProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const isEditing = !!host;
  const [formData, setFormData] = useState({
    name: host?.name || '',
    display_name: host?.display_name ?? '',
    host_group_id: host?.host_group_id != null ? String(host.host_group_id) : '',
  });
  const [hostGroups, setHostGroups] = useState<HostGroup[]>([]);

  useEffect(() => {
    orgApi.hostGroups
      .list()
      .then((res) => setHostGroups(res.data || []))
      .catch(() => setHostGroups([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [planLimit, setPlanLimit] = useState(false);
  const [nameError, setNameError] = useState('');
  // Once the user edits the name manually, stop auto-deriving it from display_name.
  const [nameEdited, setNameEdited] = useState(false);

  const validateName = (value: string): string => {
    const v = value.trim();
    if (!v) return t('metrics_view.add_host.error_name_required');
    if (v.length > 255) return t('metrics_view.add_host.error_name_too_long');
    if (!NAME_RE.test(v)) return t('metrics_view.add_host.error_name_format');
    return '';
  };

  const handleNameBlur = () => {
    if (!isEditing) setNameError(validateName(formData.name));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError(null);
    setPlanLimit(false);

    if (!isEditing) {
      const err = validateName(formData.name);
      if (err) { setNameError(err); return; }
    }

    setSubmitting(true);
    try {
      const groupId = formData.host_group_id ? Number(formData.host_group_id) : undefined;
      if (isEditing && host) {
        await orgApi.hosts.update(host.id, {
          display_name: formData.display_name || undefined,
        });
        // The update endpoint only touches display_name; group changes go through the assign endpoint.
        if (groupId && groupId !== (host.host_group_id ?? undefined)) {
          await orgApi.hosts.assignGroup(host.id, groupId);
        }
      } else {
        await orgApi.hosts.create({
          name: formData.name,
          host: formData.name,
          service: formData.name,
          display_name: formData.display_name || undefined,
          host_group_id: groupId,
        });
      }
      onSuccess();
    } catch (err: any) {
      const data = err.response?.data;
      if (data?.code === 'plan_limit') {
        setPlanLimit(true);
        setSubmitError(data.error || null);
      } else {
        setSubmitError(
          data?.error ||
            t(isEditing ? 'metrics_view.add_host.error_update' : 'metrics_view.add_host.error_create')
        );
      }
      console.error(err);
    } finally {
      setSubmitting(false);
    }
  };

  const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    const { name, value } = e.target;
    // Typing the display name auto-fills the technical name (slugified) until the
    // user edits the name field manually. Only when creating; name is fixed on edit.
    if (name === 'display_name' && !isEditing) {
      setFormData((prev) => ({
        ...prev,
        display_name: value,
        name: nameEdited ? prev.name : slugifyName(value),
      }));
      if (!nameEdited) setNameError('');
      return;
    }
    if (name === 'name') {
      setNameEdited(true);
      setNameError('');
    }
    setFormData((prev) => ({ ...prev, [name]: value }));
  };

  return (
    <form onSubmit={handleSubmit}>
      {planLimit ? (
        <PlanLimitNotice message={submitError ?? undefined} />
      ) : (
        submitError && (
          <div className='error-message' style={{ marginBottom: '1rem' }}>
            {submitError}
          </div>
        )
      )}

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: '1rem',
          marginBottom: '1.5rem',
        }}>
        <div>
          <label className='label'>
            {isEditing
              ? t('metrics_view.add_host.display_name_editing')
              : t('metrics_view.add_host.display_name')}
          </label>
          <input
            type='text'
            name='display_name'
            value={formData.display_name}
            onChange={handleChange}
            className='input'
            placeholder={
              isEditing
                ? t('metrics_view.add_host.display_name_placeholder_editing')
                : t('metrics_view.add_host.display_name_placeholder')
            }
          />
        </div>
        <div>
          <label className='label'>
            {isEditing ? t('metrics_view.add_host.name_editing') : t('metrics_view.add_host.name')}
          </label>
          <input
            type='text'
            name='name'
            value={formData.name}
            onChange={handleChange}
            onBlur={handleNameBlur}
            required
            disabled={isEditing}
            className={`input${nameError ? ' input-invalid' : ''}`}
            style={isEditing ? { opacity: 0.7, cursor: 'not-allowed' } : undefined}
            placeholder={t('metrics_view.add_host.name_placeholder')}
            title={isEditing ? t('metrics_view.add_host.name_title_editing') : undefined}
          />
          {nameError && <p className='field-error'>{nameError}</p>}
        </div>
      </div>

      <div style={{ marginBottom: '1.5rem' }}>
        <label className='label'>{t('metrics_view.add_host.group')}</label>
        <select
          name='host_group_id'
          value={formData.host_group_id}
          onChange={handleChange}
          className='input'>
          {(!isEditing || host?.host_group_id == null) && (
            <option value=''>{isEditing ? '—' : t('metrics_view.add_host.group_default_option')}</option>
          )}
          {hostGroups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>
        <p style={{ fontSize: '0.75rem', color: 'var(--text-tertiary)', marginTop: '0.25rem' }}>
          {t('metrics_view.add_host.group_hint')}
        </p>
      </div>

      <div style={{ display: 'flex', gap: '0.75rem', justifyContent: 'flex-end' }}>
        <button type='button' onClick={onCancel} className='btn btn-secondary'>
          {t('common.cancel')}
        </button>
        <button
          type='submit'
          disabled={submitting}
          className='btn btn-primary'
          style={{
            opacity: submitting ? 0.5 : 1,
            cursor: submitting ? 'not-allowed' : 'pointer',
          }}>
          {submitting
            ? isEditing
              ? t('metrics_view.add_host.submit_updating')
              : t('metrics_view.add_host.submit_creating')
            : isEditing
            ? t('metrics_view.add_host.submit_update')
            : t('metrics_view.add_host.submit_create')}
        </button>
      </div>
    </form>
  );
}
