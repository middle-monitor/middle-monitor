import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  HiOutlineRectangleGroup,
  HiOutlineTrash,
  HiOutlinePencil,
  HiOutlineServerStack,
} from 'react-icons/hi2';
import { HiPlus, HiX } from 'react-icons/hi';

import { type HostGroup } from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { useQueryRefresh } from '../hooks/useQueryRefresh';
import { RefreshControl } from '../components/RefreshControl';
import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { Skeleton, firstLoadCount } from '../components/Skeleton';
import { slugifyName } from '../utils/slugify';

const NAME_RE = /^[a-zA-Z0-9._-]+$/;

interface GroupFormProps {
  group: HostGroup | null;
  onSuccess: () => void;
  onCancel: () => void;
}

function GroupForm({ group, onSuccess, onCancel }: GroupFormProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const isEditing = !!group;
  const [formData, setFormData] = useState({
    name: group?.name || '',
    display_name: group?.display_name ?? '',
  });
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [nameError, setNameError] = useState('');
  // Once the user edits the name manually, stop auto-deriving it from display_name.
  const [nameEdited, setNameEdited] = useState(!!group);

  const validateName = (value: string): string => {
    const v = value.trim();
    if (!v) return t('metrics_view.add_host.error_name_required');
    if (v.length > 255) return t('metrics_view.add_host.error_name_too_long');
    if (!NAME_RE.test(v)) return t('metrics_view.add_host.error_name_format');
    return '';
  };

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const { name, value } = e.target;
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

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError(null);
    const err = validateName(formData.name);
    if (err) { setNameError(err); return; }

    setSubmitting(true);
    try {
      const payload = {
        name: formData.name.trim(),
        display_name: formData.display_name.trim() || undefined,
      };
      if (isEditing && group) {
        await orgApi.hostGroups.update(group.id, payload);
      } else {
        await orgApi.hostGroups.create(payload);
      }
      onSuccess();
    } catch (err: any) {
      setSubmitError(err.response?.data?.error || t('common.error'));
      console.error(err);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit}>
      {submitError && (
        <div className="error-message" style={{ marginBottom: '1rem' }}>
          {submitError}
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem', marginBottom: '1.5rem' }}>
        <div>
          <label className="label">{t('hosts.groups.form.display_name')}</label>
          <input
            type="text"
            name="display_name"
            value={formData.display_name}
            onChange={handleChange}
            className="input"
            placeholder={t('hosts.groups.form.display_name_placeholder')}
          />
        </div>
        <div>
          <label className="label">{t('hosts.groups.form.name')}</label>
          <input
            type="text"
            name="name"
            value={formData.name}
            onChange={handleChange}
            onBlur={() => setNameError(validateName(formData.name))}
            required
            className={`input${nameError ? ' input-invalid' : ''}`}
            placeholder={t('hosts.groups.form.name_placeholder')}
          />
          {nameError && <p className="field-error">{nameError}</p>}
        </div>
      </div>

      <div style={{ display: 'flex', gap: '0.75rem', justifyContent: 'flex-end' }}>
        <button type="button" onClick={onCancel} className="btn btn-secondary">
          {t('common.cancel')}
        </button>
        <button type="submit" disabled={submitting} className="btn btn-primary">
          {isEditing ? t('common.save') : t('common.create')}
        </button>
      </div>
    </form>
  );
}

function HostGroupsView() {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const { canWrite } = useAuth();
  const { orgPath } = useOrgPath();
  const [showModal, setShowModal] = useState(false);
  const [editingGroup, setEditingGroup] = useState<HostGroup | null>(null);
  const [pendingDelete, setPendingDelete] = useState<HostGroup | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // An open modal or a pending confirmation is a detail row: refreshing under
  // one is what pausing is for.
  const { refetchInterval, buildControl } = useQueryRefresh(
    'host-groups',
    0,
    showModal || pendingDelete !== null,
  );

  const groupsQueryKey = [scope, 'host-groups', 'list'];
  const groupsQuery = useQuery({
    queryKey: groupsQueryKey,
    queryFn: async () => ((await orgApi.hostGroups.list()).data as HostGroup[]) || [],
    refetchInterval,
  });

  const groups = groupsQuery.data ?? [];
  const groupValue = firstLoadCount(groupsQuery.isPending);

  const autoRefresh = buildControl({
    query: groupsQuery,
    queryKey: groupsQueryKey,
    prefix: [scope, 'host-groups'],
  });
  const { refresh } = autoRefresh;

  const handleDelete = async (group: HostGroup) => {
    setPendingDelete(null);
    try {
      await orgApi.hostGroups.delete(group.id);
      setDeleteError(null);
      refresh();
    } catch (err: any) {
      if (err.response?.status === 409) {
        setDeleteError(t('hosts.groups.delete_blocked_hosts'));
      } else {
        setDeleteError(err.response?.data?.error || t('common.error'));
      }
      console.error(err);
    }
  };

  const openCreateModal = () => {
    setEditingGroup(null);
    setShowModal(true);
  };

  const openEditModal = (group: HostGroup) => {
    setEditingGroup(group);
    setShowModal(true);
  };

  const closeModal = () => {
    setShowModal(false);
    setEditingGroup(null);
  };

  // Only the results area waits on the first load: a background refresh keeps
  // the groups on screen, and a failed one keeps showing the last good list.
  if (groupsQuery.isLoadingError) {
    return <div className="error-message">{t('common.error')}</div>;
  }

  return (
    <div>
      {/* Page Header */}
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">{t('hosts.groups.title')}</h1>
          <p className="page-subtitle">{t('hosts.groups.desc')}</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexShrink: 0 }}>
          <RefreshControl control={autoRefresh} />
          {canWrite && (
            <button
              onClick={openCreateModal}
              className="btn btn-primary"
              style={{ whiteSpace: 'nowrap', flexShrink: 0 }}
            >
              <HiPlus style={{ fontSize: '0.875rem' }} />
              <span>{t('hosts.groups.new_group')}</span>
            </button>
          )}
        </div>
      </div>

      {deleteError && (
        <div className="error-message" style={{ marginBottom: '1rem' }}>
          {deleteError}
        </div>
      )}

      {/* Groups Table */}
      <div className="card">
        <div className="card-title">
          <HiOutlineRectangleGroup className="card-title-icon" />
          {t('hosts.groups.title')} ({groupValue(groups.length)})
        </div>

        {groupsQuery.isPending ? (
          <Skeleton rows={5} />
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="table">
              <thead>
                <tr>
                  <th>{t('hosts.table.name')}</th>
                  <th>{t('hosts.groups.table_technical_name')}</th>
                  <th>{t('hosts.groups.table_hosts')}</th>
                  <th>{t('hosts.table.created')}</th>
                  {canWrite && <th>{t('common.actions')}</th>}
                </tr>
              </thead>
              <tbody>
                {groups.map((group) => {
                  const hostCount = group.host_count ?? 0;
                  const deleteBlocked = group.is_default
                    ? t('hosts.groups.cannot_delete_default')
                    : hostCount > 0
                      ? t('hosts.groups.delete_blocked_hosts')
                      : '';
                  return (
                    <tr key={group.id}>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
                          <span style={{ fontWeight: 500 }}>{group.display_name || group.name}</span>
                          {group.is_default && (
                            <span className="status-badge status-info" style={{ fontSize: '0.7rem' }}>
                              {t('hosts.groups.default')}
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        <span style={{ fontFamily: 'monospace', fontSize: '0.8125rem', color: 'var(--text-secondary)' }}>
                          {group.name}
                        </span>
                      </td>
                      <td>
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '0.35rem' }}>
                          <HiOutlineServerStack style={{ color: 'var(--text-tertiary)' }} />
                          {hostCount}
                        </span>
                      </td>
                      <td>
                        <span title={new Date(group.created_at).toLocaleString(i18n.language)}>
                          {new Date(group.created_at).toLocaleDateString(i18n.language)}
                        </span>
                      </td>
                      {canWrite && (
                        <td>
                          <div style={{ display: 'flex', gap: '0.25rem' }}>
                            <button
                              className="btn-icon"
                              onClick={() => openEditModal(group)}
                              title={t('common.edit')}
                            >
                              <HiOutlinePencil />
                            </button>
                            <button
                              className="btn-icon"
                              style={{
                                color: deleteBlocked ? 'var(--text-tertiary)' : 'var(--status-error)',
                                cursor: deleteBlocked ? 'not-allowed' : 'pointer',
                              }}
                              disabled={!!deleteBlocked}
                              onClick={() => { setDeleteError(null); setPendingDelete(group); }}
                              title={deleteBlocked || t('common.delete')}
                            >
                              <HiOutlineTrash />
                            </button>
                          </div>
                        </td>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        <p style={{ fontSize: '0.8125rem', color: 'var(--text-tertiary)', marginTop: '1rem' }}>
          {t('hosts.groups.assign_hint')} <Link to={orgPath('/hosts')}>{t('sidebar.items.hosts')}</Link>
        </p>
      </div>

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="modal-overlay" onClick={(e) => { if (e.target === e.currentTarget) closeModal(); }}>
          <div className="modal-content" style={{ maxWidth: '600px' }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                <HiPlus style={{ color: 'var(--brand-primary)' }} />
                <span>{editingGroup ? t('hosts.groups.edit_group') : t('hosts.groups.new_group')}</span>
              </h3>
              <button className="modal-close" onClick={closeModal}>
                <HiX style={{ fontSize: '1.25rem' }} />
              </button>
            </div>
            <div className="modal-body">
              <GroupForm
                group={editingGroup}
                onSuccess={() => {
                  closeModal();
                  refresh();
                }}
                onCancel={closeModal}
              />
            </div>
          </div>
        </div>
      )}

      {pendingDelete && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('hosts.groups.delete_confirm', { name: pendingDelete.display_name || pendingDelete.name })}
          onConfirm={() => handleDelete(pendingDelete)}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}

export default HostGroupsView;
