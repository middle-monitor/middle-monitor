import { useState } from 'react';
import { HiPencil, HiTrash } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import type { Service, Host } from '../../../api';
import { ConfirmDialog } from '../../../components/ConfirmDialog';

interface ServiceActionsProps {
  service: Service;
  hosts: Host[];
  onEdit: (service: Service, hosts: Host[]) => void;
  onDelete: (serviceId: number) => Promise<void>;
  onRefresh: () => void;
}

export function ServiceActions({
  service,
  hosts,
  onEdit,
  onDelete,
  onRefresh,
}: ServiceActionsProps) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);

  const handleConfirm = async () => {
    try {
      await onDelete(service.id);
      onRefresh();
    } catch {
      alert(t('metrics_view.delete_error'));
    } finally {
      setConfirmOpen(false);
    }
  };

  return (
    <>
      <div style={{ display: 'flex', gap: '0.5rem' }}>
        <button
          onClick={() => onEdit(service, hosts)}
          className='btn btn-ghost'
          style={{ padding: '0.25rem 0.5rem' }}>
          <HiPencil style={{ fontSize: '0.875rem' }} />
        </button>
        <button
          onClick={() => setConfirmOpen(true)}
          className='btn btn-danger'
          style={{ padding: '0.25rem 0.5rem' }}>
          <HiTrash style={{ fontSize: '0.875rem' }} />
        </button>
      </div>
      {confirmOpen && (
        <ConfirmDialog
          title={t('common.delete')}
          message={t('metrics_view.delete_service_confirm', { name: service.name })}
          onConfirm={handleConfirm}
          onCancel={() => setConfirmOpen(false)}
        />
      )}
    </>
  );
}
