import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { HiPlus, HiPencil, HiX } from 'react-icons/hi';
import {
  HiBolt,
  HiOutlineCircleStack,
  HiOutlineGlobeAlt,
  HiOutlineLockClosed,
  HiOutlineSignal,
  HiExclamationTriangle,
  HiArrowTopRightOnSquare,
} from 'react-icons/hi2';
import type { IconType } from 'react-icons';
import { useServiceModal } from '../contexts/ServiceModalContext';
import { type Service } from '../api';
import { useOrgApi, useOrgQueryScope } from '../hooks/useOrgApi';
import { invalidateServiceWrite } from '../queryClient';
import { slugifyName } from '../utils/slugify';
import { TagInput } from './TagInput';
import { NumberInput } from './NumberInput';
import { PlanLimitNotice } from './PlanGate';

// Service types creatable from the UI (excluding system and agent types)
const CREATABLE_SERVICE_TYPES = [
  {
    value: 'http',
    icon: HiOutlineGlobeAlt,
  },
  {
    value: 'ping',
    icon: HiBolt,
  },
  {
    value: 'sql',
    icon: HiOutlineCircleStack,
  },
  {
    value: 'certificate',
    icon: HiOutlineLockClosed,
  },
  {
    value: 'snmp',
    icon: HiOutlineSignal,
  },
] satisfies Array<{
  value: string;
  icon: IconType;
}>;

type ServiceType = (typeof CREATABLE_SERVICE_TYPES)[number]['value'];

// Default warning / critical thresholds pre-filled per check type on creation.
// SNMP has no default (the OID value is device-specific); SQL only uses critical
// (warning is derived automatically from internal heuristics).
const DEFAULT_THRESHOLDS: Record<
  string,
  { warning: number | null; critical: number | null }
> = {
  http: { warning: 1000, critical: 3000 },
  ping: { warning: 100, critical: 300 },
  sql: { warning: null, critical: 1000 },
  certificate: { warning: 30, critical: 7 },
  snmp: { warning: null, critical: null },
};

const defaultThresholdsFor = (type: string) =>
  DEFAULT_THRESHOLDS[type] ?? { warning: null, critical: null };

interface SQLCredentials {
  host: string;
  port: string;
  database: string;
  username?: string;
  password?: string;
}

export default function ServiceModal() {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const scope = useOrgQueryScope();
  const queryClient = useQueryClient();
  const { isOpen, editingService, hosts, closeModal } = useServiceModal();
  const isEditing =
    !!editingService &&
    'id' in editingService &&
    editingService.id !== undefined;

  const parseSQLCredentials = (credentials?: string | null): SQLCredentials => {
    if (!credentials) {
      return { host: '', port: '5432', database: 'postgres' };
    }
    try {
      const parsed = JSON.parse(credentials);
      return {
        host: parsed.host || '',
        port: parsed.port || '5432',
        database: parsed.database || 'postgres',
        username: parsed.username || '',
        password: parsed.password || '',
      };
    } catch {
      return { host: '', port: '5432', database: 'postgres' };
    }
  };

  const initialSQLCreds =
    editingService && editingService.type === 'sql'
      ? parseSQLCredentials(editingService.credentials)
      : { host: '', port: '5432', database: 'postgres' };

  const [formData, setFormData] = useState({
    host_id: (editingService && 'host_id' in editingService
      ? editingService.host_id
      : null) as number | null,
    name: (editingService && 'name' in editingService
      ? editingService.name
      : '') as string,
    display_name: (editingService && 'display_name' in editingService
      ? (editingService.display_name ?? '')
      : '') as string,
    type: (editingService && 'type' in editingService && editingService.type
      ? editingService.type
      : 'http') as ServiceType,
    host: (editingService && 'host' in editingService
      ? editingService.host
      : '') as string,
    path: (editingService && 'path' in editingService
      ? editingService.path || ''
      : '') as string,
    service_interval: (editingService && 'service_interval' in editingService
      ? editingService.service_interval
      : 60) as number,
    max_attempts: (editingService && 'max_attempts' in editingService
      ? editingService.max_attempts
      : 3) as number,
    warning_threshold: (editingService && 'warning_threshold' in editingService
      ? (editingService.warning_threshold ?? null)
      : null) as number | null,
    critical_threshold: (editingService &&
    'critical_threshold' in editingService
      ? (editingService.critical_threshold ?? null)
      : null) as number | null,
    expected_status_code: (editingService &&
    'expected_status_code' in editingService
      ? (editingService.expected_status_code ?? null)
      : null) as number | null,
    expected_body_contains: (editingService &&
    'expected_body_contains' in editingService
      ? (editingService.expected_body_contains ?? '')
      : '') as string,
    expected_body_mode: (editingService &&
    'expected_body_mode' in editingService
      ? editingService.expected_body_mode || 'contains'
      : 'contains') as 'contains' | 'json_path',
    sql_host: initialSQLCreds.host,
    sql_port: initialSQLCreds.port,
    sql_database: initialSQLCreds.database,
    sql_username: initialSQLCreds.username || '',
    sql_password: initialSQLCreds.password || '',
    snmp_community: '',
    snmp_oid: '',
    http_auth_mode: (() => {
      if (
        editingService &&
        editingService.type === 'http' &&
        'http_auth_mode' in editingService &&
        editingService.http_auth_mode
      ) {
        const m = editingService.http_auth_mode;
        if (m === 'bearer' || m === 'basic' || m === 'none') return m;
      }
      return 'none' as 'none' | 'bearer' | 'basic';
    })(),
    http_bearer_token: '',
    http_basic_user: '',
    http_basic_password: '',
    http_auth_configured: !!(
      editingService &&
      'http_auth_configured' in editingService &&
      editingService.http_auth_configured
    ),
  });

  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [planLimit, setPlanLimit] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // Once the user edits the name manually, stop auto-deriving it from display_name.
  const [nameEdited, setNameEdited] = useState(false);

  useEffect(() => {
    if (isOpen && editingService) {
      const sqlCreds =
        editingService.type === 'sql'
          ? parseSQLCredentials(editingService.credentials)
          : null;
      setFormData({
        host_id:
          'host_id' in editingService ? editingService.host_id || null : null,
        name: 'name' in editingService ? editingService.name || '' : '',
        display_name:
          'display_name' in editingService
            ? (editingService.display_name ?? '')
            : '',
        type: (editingService.type as ServiceType) || 'http',
        host: 'host' in editingService ? editingService.host || '' : '',
        path: 'path' in editingService ? editingService.path || '' : '',
        service_interval:
          'service_interval' in editingService
            ? editingService.service_interval || 60
            : 60,
        max_attempts:
          'max_attempts' in editingService
            ? editingService.max_attempts || 3
            : 3,
        warning_threshold:
          'warning_threshold' in editingService
            ? (editingService.warning_threshold ?? null)
            : null,
        critical_threshold:
          'critical_threshold' in editingService
            ? (editingService.critical_threshold ?? null)
            : null,
        expected_status_code:
          'expected_status_code' in editingService
            ? (editingService.expected_status_code ?? null)
            : null,
        expected_body_contains:
          'expected_body_contains' in editingService
            ? (editingService.expected_body_contains ?? '')
            : '',
        expected_body_mode: ('expected_body_mode' in editingService
          ? editingService.expected_body_mode || 'contains'
          : 'contains') as 'contains' | 'json_path',
        sql_host: sqlCreds?.host || '',
        sql_port: sqlCreds?.port || '5432',
        sql_database: sqlCreds?.database || 'postgres',
        sql_username: sqlCreds?.username || '',
        sql_password: sqlCreds?.password || '',
        snmp_community: '',
        snmp_oid: '',
        http_auth_mode: (() => {
          if (
            editingService.type === 'http' &&
            'http_auth_mode' in editingService &&
            editingService.http_auth_mode
          ) {
            const m = editingService.http_auth_mode;
            if (m === 'bearer' || m === 'basic' || m === 'none') return m;
          }
          return 'none';
        })() as 'none' | 'bearer' | 'basic',
        http_bearer_token: '',
        http_basic_user: '',
        http_basic_password: '',
        http_auth_configured: !!(
          'http_auth_configured' in editingService &&
          editingService.http_auth_configured
        ),
      });
    } else if (isOpen && !editingService) {
      // Reset form when opening for new service (preselect if a single host)
      const onlyHost = hosts.length === 1 ? hosts[0] : null;
      setFormData({
        host_id: onlyHost ? onlyHost.id : null,
        name: '',
        display_name: '',
        type: 'http',
        // Default the HTTP URL to the single host's address when auto-selected.
        host: onlyHost ? onlyHost.host : '',
        path: '',
        service_interval: 60,
        max_attempts: 3,
        warning_threshold: defaultThresholdsFor('http').warning,
        critical_threshold: defaultThresholdsFor('http').critical,
        expected_status_code: null,
        expected_body_contains: '',
        expected_body_mode: 'contains',
        sql_host: '',
        sql_port: '5432',
        sql_database: 'postgres',
        sql_username: '',
        sql_password: '',
        snmp_community: '',
        snmp_oid: '',
        http_auth_mode: 'none',
        http_bearer_token: '',
        http_basic_user: '',
        http_basic_password: '',
        http_auth_configured: false,
      });
    }
    setError(null);
    setFieldErrors({});
    setNameEdited(false);
    // hosts is intentionally omitted: form must only reset when the modal opens,
    // not every time the host list refreshes in the background.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, editingService]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setPlanLimit(false);
    setSubmitting(true);

    try {
      if (!formData.host_id) {
        setError(t('service_modal.errors.host_required'));
        setSubmitting(false);
        return;
      }

      if (!formData.name.trim()) {
        setError(t('service_modal.errors.name_required'));
        setSubmitting(false);
        return;
      }

      if (formData.name.trim().length > 255) {
        setError(t('service_modal.errors.name_too_long'));
        setSubmitting(false);
        return;
      }

      const interval = formData.service_interval || 60;
      if (interval < 60 || interval > 3600) {
        setError(t('service_modal.errors.interval_out_of_range'));
        setSubmitting(false);
        return;
      }

      const attempts = formData.max_attempts || 3;
      if (attempts < 1 || attempts > 10) {
        setError(t('service_modal.errors.attempts_out_of_range'));
        setSubmitting(false);
        return;
      }

      if (formData.type === 'sql') {
        if (!formData.sql_host.trim() || !formData.sql_database.trim()) {
          setError(t('service_modal.errors.sql_host_db_required'));
          setSubmitting(false);
          return;
        }
        const port = parseInt(formData.sql_port, 10);
        if (isNaN(port) || port < 1 || port > 65535) {
          setError(t('service_modal.errors.sql_port_invalid'));
          setSubmitting(false);
          return;
        }
      } else if (formData.type === 'certificate') {
        if (!formData.host.trim()) {
          setError(t('service_modal.errors.cert_host_required'));
          setSubmitting(false);
          return;
        }
      } else if (formData.type === 'snmp') {
        if (!formData.host.trim()) {
          setError(t('service_modal.errors.snmp_host_required'));
          setSubmitting(false);
          return;
        }
      } else if (formData.type === 'http') {
        if (!formData.host.trim()) {
          setError(t('service_modal.errors.http_url_required'));
          setSubmitting(false);
          return;
        }
        if (formData.http_auth_mode === 'bearer') {
          const hasToken = !!formData.http_bearer_token.trim();
          if (!hasToken && (!isEditing || !formData.http_auth_configured)) {
            setError(t('service_modal.errors.bearer_token_required'));
            setSubmitting(false);
            return;
          }
        }
        if (formData.http_auth_mode === 'basic') {
          const u = formData.http_basic_user.trim();
          const p = formData.http_basic_password.trim();
          if (!isEditing || !formData.http_auth_configured) {
            if (!u || !p) {
              setError(t('service_modal.errors.basic_auth_required'));
              setSubmitting(false);
              return;
            }
          }
        }
      } else if (formData.type === 'ping') {
        if (!formData.host.trim()) {
          setError(t('service_modal.errors.ping_host_required'));
          setSubmitting(false);
          return;
        }
      }

      const selectedHost = hosts.find((h) => h.id === formData.host_id);
      if (!selectedHost) {
        setError(t('service_modal.errors.host_not_found'));
        setSubmitting(false);
        return;
      }

      const serviceData: Partial<Service> = {
        name: formData.name.trim(),
        display_name: formData.display_name.trim() || undefined,
        type: formData.type,
        host_id: formData.host_id!,
        service: selectedHost.service,
        service_interval: formData.service_interval || 60,
        max_attempts: formData.max_attempts || 3,
        warning_threshold: formData.warning_threshold,
        critical_threshold: formData.critical_threshold,
      };

      if (formData.type === 'http') {
        serviceData.host = formData.host.trim();
        if (formData.path.trim()) {
          serviceData.path = formData.path.trim();
        }
        serviceData.expected_status_code = formData.expected_status_code;
        serviceData.expected_body_contains =
          formData.expected_body_contains.trim() || null;
        serviceData.expected_body_mode = formData.expected_body_contains.trim()
          ? formData.expected_body_mode
          : null;
        const httpAuth: Record<string, string> = {
          mode: formData.http_auth_mode,
        };
        let preserveSecrets = false;
        if (formData.http_auth_mode === 'bearer') {
          if (formData.http_bearer_token.trim()) {
            httpAuth.bearer_token = formData.http_bearer_token.trim();
          } else if (isEditing && formData.http_auth_configured) {
            preserveSecrets = true;
          }
        } else if (formData.http_auth_mode === 'basic') {
          if (formData.http_basic_user.trim()) {
            httpAuth.basic_user = formData.http_basic_user.trim();
          }
          if (formData.http_basic_password.trim()) {
            httpAuth.basic_password = formData.http_basic_password.trim();
          }
          if (
            isEditing &&
            formData.http_auth_configured &&
            (!httpAuth.basic_user || !httpAuth.basic_password)
          ) {
            preserveSecrets = true;
          }
        }
        serviceData.credentials = JSON.stringify({ http_auth: httpAuth });
        if (preserveSecrets) {
          serviceData.preserve_http_secrets = true;
        }
      } else if (formData.type === 'sql') {
        serviceData.host = formData.sql_host.trim();
        const sqlCreds: SQLCredentials = {
          host: formData.sql_host.trim(),
          port: formData.sql_port || '5432',
          database: formData.sql_database.trim(),
        };
        if (formData.sql_username.trim()) {
          sqlCreds.username = formData.sql_username.trim();
        }
        if (formData.sql_password.trim()) {
          sqlCreds.password = formData.sql_password.trim();
        }
        serviceData.credentials = JSON.stringify(sqlCreds);
      } else if (formData.type === 'ping') {
        serviceData.host = formData.host.trim();
      } else if (formData.type === 'certificate') {
        serviceData.host = formData.host.trim();
      } else if (formData.type === 'snmp') {
        serviceData.host = formData.host.trim();
        if (formData.snmp_community.trim() || formData.snmp_oid.trim()) {
          const snmpCreds: any = {};
          if (formData.snmp_community.trim()) {
            snmpCreds.community = formData.snmp_community.trim();
          }
          if (formData.snmp_oid.trim()) {
            snmpCreds.oid = formData.snmp_oid.trim();
          }
          serviceData.credentials = JSON.stringify(snmpCreds);
        }
      }

      if (
        isEditing &&
        editingService &&
        'id' in editingService &&
        editingService.id
      ) {
        await orgApi.services.update(editingService.id, serviceData);
      } else {
        await orgApi.services.create(
          serviceData as Omit<Service, 'id' | 'created_at'>,
        );
      }

      closeModal();
      invalidateServiceWrite(queryClient, scope);
    } catch (err: any) {
      const data = err.response?.data;
      if (data?.code === 'plan_limit') {
        setPlanLimit(true);
        setError(data.error || null);
      } else {
        setError(
          data?.error ||
            t(
              isEditing
                ? 'service_modal.errors.save_update'
                : 'service_modal.errors.save_create',
            ),
        );
      }
      console.error(err);
    } finally {
      setSubmitting(false);
    }
  };

  const validateServiceField = (
    name: string,
    value: string | number,
  ): string => {
    if (name === 'name') {
      const v = String(value).trim();
      if (!v) return t('service_modal.errors.name_required');
      if (v.length > 255) return t('service_modal.errors.name_too_long');
    }
    if (name === 'service_interval') {
      const n = Number(value);
      if (n < 60 || n > 3600)
        return t('service_modal.errors.interval_out_of_range');
    }
    if (name === 'max_attempts') {
      const n = Number(value);
      if (n < 1 || n > 10)
        return t('service_modal.errors.attempts_out_of_range');
    }
    if (name === 'sql_port') {
      const n = parseInt(String(value), 10);
      if (isNaN(n) || n < 1 || n > 65535)
        return t('service_modal.errors.sql_port_invalid');
    }
    if (name === 'sql_host' && !String(value).trim())
      return t('service_modal.errors.sql_host_db_required');
    if (name === 'sql_database' && !String(value).trim())
      return t('service_modal.errors.sql_host_db_required');
    if (name === 'host' && formData.type === 'http' && !String(value).trim())
      return t('service_modal.errors.http_url_required');
    if (name === 'host' && formData.type === 'ping' && !String(value).trim())
      return t('service_modal.errors.ping_host_required');
    return '';
  };

  const handleFieldBlur = (name: string, value: string | number) => {
    const err = validateServiceField(name, value);
    setFieldErrors((prev) => ({ ...prev, [name]: err }));
  };

  const clearFieldError = (name: string) => {
    if (fieldErrors[name]) setFieldErrors((prev) => ({ ...prev, [name]: '' }));
  };

  const handleChange = (
    e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>,
  ) => {
    const { name, value } = e.target;
    clearFieldError(name);

    // Typing the display name auto-fills the technical name (slugified) until the
    // user edits the name field manually. Only when creating a new service.
    if (name === 'display_name' && !isEditing) {
      setFormData((prev) => ({
        ...prev,
        display_name: value,
        name: nameEdited ? prev.name : slugifyName(value),
      }));
      if (!nameEdited) clearFieldError('name');
      return;
    }
    if (name === 'name') {
      setNameEdited(true);
    }

    if (name === 'host_id') {
      const hostId = value === '' ? null : Number(value);
      const selected = hostId ? hosts.find((h) => h.id === hostId) : null;
      setFormData((prev) => ({
        ...prev,
        host_id: hostId,
        // For HTTP checks, default the URL to the host address until the user
        // types their own; other types keep their own host/URL fields.
        host:
          (prev.type === 'http' || prev.type === 'ping') &&
          !prev.host &&
          selected
            ? selected.host
            : prev.host,
      }));
      return;
    }

    if (name === 'type') {
      const defaults = defaultThresholdsFor(value);
      setFormData((prev) => ({
        ...prev,
        type: value as ServiceType,
        warning_threshold: defaults.warning,
        critical_threshold: defaults.critical,
        sql_host: '',
        sql_port: '5432',
        sql_database: 'postgres',
        sql_username: '',
        sql_password: '',
        snmp_community: '',
        snmp_oid: '',
        path: prev.type === 'http' ? prev.path : '',
      }));
      return;
    }

    setFormData((prev) => ({
      ...prev,
      [name]:
        name === 'service_interval' ||
        name === 'max_attempts' ||
        name === 'sql_port'
          ? Number(value) || (name === 'sql_port' ? '5432' : 0)
          : value,
    }));
  };

  if (!isOpen) return null;

  const selectedType = CREATABLE_SERVICE_TYPES.find(
    (type) => type.value === formData.type,
  );
  const SelectedTypeIcon = selectedType?.icon;
  const selectedTypeDescription = selectedType
    ? t(`service_modal.types.${selectedType.value}.description`)
    : null;

  const selectedHost = formData.host_id
    ? hosts.find((h) => h.id === formData.host_id)
    : null;

  const leaveUnchangedPlaceholder = t(
    'service_modal.placeholders.leave_unchanged',
  );
  const serviceTypeStr = formData.type as string;
  const thresholdLabel =
    serviceTypeStr === 'certificate'
      ? t('service_modal.labels.alert_threshold_cert')
      : serviceTypeStr === 'sql'
        ? t('service_modal.labels.alert_threshold_sql')
        : serviceTypeStr === 'snmp'
          ? t('service_modal.labels.alert_threshold_snmp')
          : serviceTypeStr.startsWith('error_service')
            ? t('service_modal.labels.alert_threshold_errors')
            : serviceTypeStr.startsWith('trace_service')
              ? t('service_modal.labels.alert_threshold_latency')
              : serviceTypeStr === 'http'
                ? t('service_modal.labels.alert_threshold_http')
                : t('service_modal.labels.alert_threshold_default');
  const thresholdPlaceholder =
    serviceTypeStr === 'certificate'
      ? t('service_modal.placeholders.threshold_cert')
      : serviceTypeStr === 'agent_cpu' ||
          serviceTypeStr === 'agent_ram' ||
          serviceTypeStr === 'agent_disk'
        ? t('service_modal.placeholders.threshold_percent')
        : serviceTypeStr === 'agent_network'
          ? t('service_modal.placeholders.threshold_network')
          : serviceTypeStr === 'http'
            ? t('service_modal.placeholders.threshold_http')
            : serviceTypeStr === 'sql'
              ? t('service_modal.placeholders.threshold_latency')
              : serviceTypeStr.startsWith('error_service')
                ? t('service_modal.placeholders.threshold_errors')
                : serviceTypeStr.startsWith('trace_service')
                  ? t('service_modal.placeholders.threshold_latency')
                  : t('service_modal.placeholders.threshold_default');
  const thresholdHint =
    serviceTypeStr === 'certificate'
      ? t('service_modal.labels.threshold_hint_cert')
      : serviceTypeStr === 'sql'
        ? t('service_modal.labels.threshold_hint_sql')
        : serviceTypeStr === 'snmp'
          ? t('service_modal.labels.threshold_hint_snmp')
          : serviceTypeStr.startsWith('error_service')
            ? t('service_modal.labels.threshold_hint_errors')
            : serviceTypeStr.startsWith('trace_service')
              ? t('service_modal.labels.threshold_hint_traces')
              : serviceTypeStr === 'http'
                ? t('service_modal.labels.threshold_hint_http')
                : t('service_modal.labels.threshold_hint_default');

  return (
    <div
      className='modal-overlay'
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          closeModal();
        }
      }}>
      <div
        className='modal-content'
        style={{ maxWidth: '700px' }}
        onClick={(e) => e.stopPropagation()}>
        {/* Modal Header */}
        <div className='modal-header'>
          <h3
            className='modal-title'
            style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            {isEditing ? (
              <>
                <HiPencil style={{ color: 'var(--brand-primary)' }} />
                <span>{t('service_modal.title_edit')}</span>
              </>
            ) : (
              <>
                <HiPlus style={{ color: 'var(--brand-primary)' }} />
                <span>{t('service_modal.title_add')}</span>
              </>
            )}
          </h3>
          <button className='modal-close' onClick={closeModal}>
            <HiX style={{ fontSize: '1.25rem' }} />
          </button>
        </div>

        {/* Modal Body */}
        <form onSubmit={handleSubmit}>
          <div className='modal-body'>
            {selectedType && SelectedTypeIcon && (
              <p
                style={{
                  color: 'var(--text-secondary)',
                  fontSize: '0.875rem',
                  margin: '0 0 1.5rem 0',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  flexWrap: 'wrap',
                }}>
                <SelectedTypeIcon
                  style={{
                    color: 'var(--brand-primary)',
                    flexShrink: 0,
                    width: '1.125rem',
                    height: '1.125rem',
                  }}
                />
                <span>{selectedTypeDescription}</span>
                <a
                  href={`/docs#check-${formData.type}`}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{
                    color: 'var(--brand-primary)',
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: '0.25rem',
                    fontWeight: 500,
                  }}>
                  {t('service_modal.types.doc_link')}
                  <HiArrowTopRightOnSquare
                    style={{ width: '0.875rem', height: '0.875rem' }}
                  />
                </a>
              </p>
            )}

            {planLimit ? (
              <PlanLimitNotice message={error ?? undefined} />
            ) : (
              error && (
                <div className='error-message' style={{ marginBottom: '1rem' }}>
                  {error}
                </div>
              )
            )}

            {/* Host: read-only when editing if the host is known; otherwise a dropdown */}
            {isEditing && selectedHost ? (
              <div style={{ marginBottom: '1.5rem' }}>
                <label className='label'>
                  {t('service_modal.labels.host')}
                </label>
                <div
                  style={{
                    padding: '0.75rem',
                    background: 'var(--bg-secondary)',
                    border: '1px solid var(--border-primary)',
                    borderRadius: '6px',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '0.5rem',
                  }}>
                  <span
                    style={{ fontWeight: 600, color: 'var(--brand-primary)' }}>
                    {selectedHost.name}
                  </span>
                  <span style={{ color: 'var(--text-tertiary)' }}>
                    ({selectedHost.host})
                  </span>
                  <span
                    style={{
                      color: 'var(--text-tertiary)',
                      marginLeft: 'auto',
                    }}>
                    {selectedHost.service}
                  </span>
                </div>
              </div>
            ) : hosts.length > 0 ? (
              <div style={{ marginBottom: '1.5rem' }}>
                <label className='label' htmlFor='service-modal-host-id'>
                  {t('service_modal.labels.host')}
                </label>
                <select
                  id='service-modal-host-id'
                  name='host_id'
                  value={formData.host_id ?? ''}
                  onChange={handleChange}
                  required
                  className='select'>
                  <option value=''>
                    {t('service_modal.labels.choose_host')}
                  </option>
                  {hosts.map((h) => (
                    <option key={h.id} value={h.id}>
                      {h.name} ({h.host}) — {h.service}
                    </option>
                  ))}
                </select>
                <p
                  style={{
                    color: 'var(--text-tertiary)',
                    fontSize: '0.75rem',
                    marginTop: '0.5rem',
                    marginBottom: 0,
                  }}>
                  {t('service_modal.labels.host_belongs_hint')}
                </p>
              </div>
            ) : (
              <div className='error-message' style={{ marginBottom: '1.5rem' }}>
                {t('service_modal.labels.no_hosts')}
              </div>
            )}

            {/* Service Display Name (auto-fills the technical name below) */}
            <div style={{ marginBottom: '1.5rem' }}>
              <label className='label'>
                {t('service_modal.labels.display_name')}
              </label>
              <input
                type='text'
                name='display_name'
                value={formData.display_name}
                onChange={handleChange}
                className='input'
                placeholder={t('service_modal.placeholders.display_name')}
              />
            </div>

            {/* Service Name and Type */}
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: '2fr 1fr',
                gap: '1rem',
                marginBottom: '1.5rem',
              }}>
              <div>
                <label className='label'>
                  {t('service_modal.labels.service_name')}
                </label>
                <input
                  type='text'
                  name='name'
                  value={formData.name}
                  onChange={handleChange}
                  onBlur={(e) => handleFieldBlur('name', e.target.value)}
                  required
                  className={`input${fieldErrors.name ? ' input-invalid' : ''}`}
                  placeholder={t('service_modal.placeholders.service_name')}
                />
                {fieldErrors.name && (
                  <p className='field-error'>{fieldErrors.name}</p>
                )}
              </div>

              <div>
                <label className='label'>
                  {t('service_modal.labels.check_type')}
                </label>
                <select
                  name='type'
                  value={formData.type}
                  onChange={handleChange}
                  required
                  disabled={isEditing}
                  className='select'
                  style={{
                    opacity: isEditing ? 0.6 : 1,
                    cursor: isEditing ? 'not-allowed' : 'pointer',
                  }}>
                  {CREATABLE_SERVICE_TYPES.map((type) => (
                    <option key={type.value} value={type.value}>
                      {t(`service_modal.types.${type.value}.label`)}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            {/* HTTP Check Fields */}
            {formData.type === 'http' && (
              <>
                <div style={{ marginBottom: '1.5rem' }}>
                  <label className='label'>
                    {t('service_modal.labels.url')}
                  </label>
                  <input
                    type='text'
                    name='host'
                    value={formData.host}
                    onChange={handleChange}
                    onBlur={(e) => handleFieldBlur('host', e.target.value)}
                    required
                    className={`input${fieldErrors.host ? ' input-invalid' : ''}`}
                    placeholder={t('service_modal.placeholders.url')}
                  />
                  {fieldErrors.host && (
                    <p className='field-error'>{fieldErrors.host}</p>
                  )}
                </div>

                <div style={{ marginBottom: '1.5rem' }}>
                  <label className='label'>
                    {t('service_modal.labels.path_optional')}
                  </label>
                  <input
                    type='text'
                    name='path'
                    value={formData.path}
                    onChange={handleChange}
                    className='input'
                    placeholder={t('service_modal.placeholders.path')}
                  />
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {t('service_modal.labels.path_hint')}
                  </p>
                </div>

                <div style={{ marginBottom: '1.5rem' }}>
                  <label className='label'>
                    {t('service_modal.labels.expected_status_code')}
                  </label>
                  <NumberInput
                    name='expected_status_code'
                    value={
                      formData.expected_status_code === null
                        ? ''
                        : formData.expected_status_code
                    }
                    onChange={(e) =>
                      setFormData((prev) => ({
                        ...prev,
                        expected_status_code: e.target.value
                          ? Number(e.target.value)
                          : null,
                      }))
                    }
                    min={100}
                    max={999}
                    className='input'
                    placeholder={t(
                      'service_modal.placeholders.expected_status_code',
                    )}
                  />
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {t('service_modal.labels.expected_status_code_hint')}
                  </p>
                </div>

                <div style={{ marginBottom: '1.5rem' }}>
                  <label className='label'>
                    {t('service_modal.labels.expected_body_contains')}
                  </label>
                  <div
                    style={{
                      display: 'flex',
                      gap: '0.5rem',
                      alignItems: 'flex-start',
                    }}>
                    <select
                      className='select'
                      style={{ maxWidth: 200, flexShrink: 0 }}
                      value={formData.expected_body_mode}
                      onChange={(e) =>
                        setFormData((prev) => ({
                          ...prev,
                          expected_body_mode: e.target.value as
                            | 'contains'
                            | 'json_path',
                        }))
                      }>
                      <option value='contains'>
                        {t('service_modal.labels.body_mode_contains')}
                      </option>
                      <option value='json_path'>
                        {t('service_modal.labels.body_mode_json_path')}
                      </option>
                    </select>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <TagInput
                        separator={'\n'}
                        value={formData.expected_body_contains}
                        onChange={(v) =>
                          setFormData((prev) => ({
                            ...prev,
                            expected_body_contains: v,
                          }))
                        }
                        placeholder={
                          formData.expected_body_mode === 'json_path'
                            ? t('service_modal.placeholders.expected_body_json')
                            : t(
                                'service_modal.placeholders.expected_body_contains',
                              )
                        }
                      />
                    </div>
                  </div>
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {formData.expected_body_mode === 'json_path'
                      ? t('service_modal.labels.expected_body_json_hint')
                      : t('service_modal.labels.expected_body_contains_hint')}
                  </p>
                </div>

                <div style={{ marginBottom: '1.5rem' }}>
                  <label className='label'>
                    {t('service_modal.labels.http_auth_optional')}
                  </label>
                  <select
                    name='http_auth_mode'
                    value={formData.http_auth_mode}
                    onChange={handleChange}
                    className='select'>
                    <option value='none'>
                      {t('service_modal.labels.http_auth_none')}
                    </option>
                    <option value='bearer'>
                      {t('service_modal.labels.http_auth_bearer')}
                    </option>
                    <option value='basic'>
                      {t('service_modal.labels.http_auth_basic')}
                    </option>
                  </select>
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {t('service_modal.labels.http_auth_secrets_hint')}
                  </p>
                </div>

                {formData.http_auth_mode === 'bearer' && (
                  <div style={{ marginBottom: '1.5rem' }}>
                    <label className='label'>
                      {t('service_modal.labels.bearer_token')}
                    </label>
                    <input
                      type='password'
                      name='http_bearer_token'
                      value={formData.http_bearer_token}
                      onChange={handleChange}
                      className='input'
                      autoComplete='off'
                      placeholder={
                        isEditing && formData.http_auth_configured
                          ? leaveUnchangedPlaceholder
                          : t('service_modal.placeholders.bearer_token')
                      }
                    />
                  </div>
                )}

                {formData.http_auth_mode === 'basic' && (
                  <div
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '1fr 1fr',
                      gap: '1rem',
                      marginBottom: '1.5rem',
                    }}>
                    <div>
                      <label className='label'>
                        {t('service_modal.labels.username')}
                      </label>
                      <input
                        type='text'
                        name='http_basic_user'
                        value={formData.http_basic_user}
                        onChange={handleChange}
                        className='input'
                        autoComplete='off'
                        placeholder={
                          isEditing && formData.http_auth_configured
                            ? leaveUnchangedPlaceholder
                            : t('service_modal.placeholders.username')
                        }
                      />
                    </div>
                    <div>
                      <label className='label'>
                        {t('service_modal.labels.password')}
                      </label>
                      <input
                        type='password'
                        name='http_basic_password'
                        value={formData.http_basic_password}
                        onChange={handleChange}
                        className='input'
                        autoComplete='new-password'
                        placeholder={
                          isEditing && formData.http_auth_configured
                            ? leaveUnchangedPlaceholder
                            : t('service_modal.placeholders.password')
                        }
                      />
                    </div>
                  </div>
                )}
              </>
            )}

            {/* Ping Check Fields */}
            {formData.type === 'ping' && (
              <div style={{ marginBottom: '1.5rem' }}>
                <label className='label'>
                  {t('service_modal.labels.ping_host')}
                </label>
                <input
                  type='text'
                  name='host'
                  value={formData.host}
                  onChange={handleChange}
                  onBlur={(e) => handleFieldBlur('host', e.target.value)}
                  required
                  className={`input${fieldErrors.host ? ' input-invalid' : ''}`}
                  placeholder={t('service_modal.placeholders.ping_host')}
                />
                {fieldErrors.host && (
                  <p className='field-error'>{fieldErrors.host}</p>
                )}
                <p
                  style={{
                    color: 'var(--text-tertiary)',
                    fontSize: '0.75rem',
                    marginTop: '0.5rem',
                  }}>
                  {t('service_modal.labels.ping_info')}
                </p>
              </div>
            )}

            {/* SQL Check Fields */}
            {formData.type === 'sql' && (
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1fr 1fr',
                  gap: '1rem',
                  marginBottom: '1.5rem',
                }}>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.host')}
                  </label>
                  <input
                    type='text'
                    name='sql_host'
                    value={formData.sql_host}
                    onChange={handleChange}
                    onBlur={(e) => handleFieldBlur('sql_host', e.target.value)}
                    required
                    className={`input${fieldErrors.sql_host ? ' input-invalid' : ''}`}
                    placeholder={t('service_modal.placeholders.sql_host')}
                  />
                  {fieldErrors.sql_host && (
                    <p className='field-error'>{fieldErrors.sql_host}</p>
                  )}
                </div>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.port')}
                  </label>
                  <NumberInput
                    name='sql_port'
                    value={formData.sql_port}
                    onChange={handleChange}
                    onBlur={(e) => handleFieldBlur('sql_port', e.target.value)}
                    required
                    className={`input${fieldErrors.sql_port ? ' input-invalid' : ''}`}
                    placeholder='5432'
                  />
                  {fieldErrors.sql_port && (
                    <p className='field-error'>{fieldErrors.sql_port}</p>
                  )}
                </div>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.database')}
                  </label>
                  <input
                    type='text'
                    name='sql_database'
                    value={formData.sql_database}
                    onChange={handleChange}
                    onBlur={(e) =>
                      handleFieldBlur('sql_database', e.target.value)
                    }
                    required
                    className={`input${fieldErrors.sql_database ? ' input-invalid' : ''}`}
                    placeholder={t('service_modal.placeholders.sql_database')}
                  />
                  {fieldErrors.sql_database && (
                    <p className='field-error'>{fieldErrors.sql_database}</p>
                  )}
                </div>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.username_optional')}
                  </label>
                  <input
                    type='text'
                    name='sql_username'
                    value={formData.sql_username}
                    onChange={handleChange}
                    className='input'
                    placeholder={t('service_modal.placeholders.sql_database')}
                  />
                </div>
                <div style={{ gridColumn: '1 / -1' }}>
                  <label className='label'>
                    {t('service_modal.labels.password_optional')}
                  </label>
                  <input
                    type='password'
                    name='sql_password'
                    value={formData.sql_password}
                    onChange={handleChange}
                    className='input'
                    placeholder={t('service_modal.placeholders.password')}
                  />
                </div>
              </div>
            )}

            {/* Certificate Check Fields */}
            {formData.type === 'certificate' && (
              <div style={{ marginBottom: '1.5rem' }}>
                <label className='label'>
                  {t('service_modal.labels.host_domain')}
                </label>
                <input
                  type='text'
                  name='host'
                  value={formData.host}
                  onChange={handleChange}
                  required
                  className='input'
                  placeholder={t('service_modal.placeholders.cert_host')}
                />
                <p
                  style={{
                    color: 'var(--text-tertiary)',
                    fontSize: '0.75rem',
                    marginTop: '0.5rem',
                  }}>
                  {t('service_modal.labels.cert_hint')}
                </p>
              </div>
            )}

            {/* SNMP Check Fields */}
            {formData.type === 'snmp' && (
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1fr 1fr',
                  gap: '1rem',
                  marginBottom: '1.5rem',
                }}>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.host')}
                  </label>
                  <input
                    type='text'
                    name='host'
                    value={formData.host}
                    onChange={handleChange}
                    required
                    className='input'
                    placeholder={t('service_modal.placeholders.snmp_host')}
                  />
                </div>
                <div>
                  <label className='label'>
                    {t('service_modal.labels.community_optional')}
                  </label>
                  <input
                    type='text'
                    name='snmp_community'
                    value={formData.snmp_community}
                    onChange={handleChange}
                    className='input'
                    placeholder={t('service_modal.placeholders.snmp_community')}
                  />
                </div>
                <div style={{ gridColumn: '1 / -1' }}>
                  <label className='label'>
                    {t('service_modal.labels.oid_optional')}
                  </label>
                  <input
                    type='text'
                    name='snmp_oid'
                    value={formData.snmp_oid}
                    onChange={handleChange}
                    className='input'
                    placeholder={t('service_modal.placeholders.snmp_oid')}
                  />
                </div>
              </div>
            )}

            {/* Interval and Max Attempts */}
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr 1fr 1fr',
                gap: '1rem',
                marginBottom: '1.5rem',
              }}>
              <div>
                <label
                  className='label'
                  style={{
                    minHeight: '2.45rem',
                    display: 'flex',
                    alignItems: 'flex-end',
                  }}>
                  {t('service_modal.labels.interval')}
                </label>
                <NumberInput
                  name='service_interval'
                  value={formData.service_interval}
                  onChange={handleChange}
                  onBlur={(e) =>
                    handleFieldBlur('service_interval', e.target.value)
                  }
                  required
                  min={60}
                  max={3600}
                  className={`input${fieldErrors.service_interval ? ' input-invalid' : ''}`}
                />
                {fieldErrors.service_interval ? (
                  <p className='field-error'>{fieldErrors.service_interval}</p>
                ) : (
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {t('service_modal.labels.interval_hint')}
                  </p>
                )}
              </div>

              <div>
                <label
                  className='label'
                  style={{
                    minHeight: '2.45rem',
                    display: 'flex',
                    alignItems: 'flex-end',
                  }}>
                  {t('service_modal.labels.max_attempts')}
                </label>
                <NumberInput
                  name='max_attempts'
                  value={formData.max_attempts}
                  onChange={handleChange}
                  onBlur={(e) =>
                    handleFieldBlur('max_attempts', e.target.value)
                  }
                  required
                  min={1}
                  max={10}
                  className={`input${fieldErrors.max_attempts ? ' input-invalid' : ''}`}
                />
                {fieldErrors.max_attempts ? (
                  <p className='field-error'>{fieldErrors.max_attempts}</p>
                ) : (
                  <p
                    style={{
                      color: 'var(--text-tertiary)',
                      fontSize: '0.75rem',
                      marginTop: '0.5rem',
                    }}>
                    {t('service_modal.labels.max_attempts_hint')}
                  </p>
                )}
              </div>

              <div>
                <label
                  className='label'
                  style={{
                    minHeight: '2.45rem',
                    display: 'flex',
                    alignItems: 'flex-end',
                  }}>
                  {thresholdLabel}{' '}
                  {t('service_modal.labels.alert_threshold_optional')}
                </label>
                <div style={{ display: 'flex', gap: '0.75rem' }}>
                  {serviceTypeStr !== 'sql' && (
                    <div style={{ flex: 1 }}>
                      <NumberInput
                        name='warning_threshold'
                        value={
                          formData.warning_threshold === null
                            ? ''
                            : formData.warning_threshold
                        }
                        onChange={(e) =>
                          setFormData((prev) => ({
                            ...prev,
                            warning_threshold: e.target.value
                              ? Number(e.target.value)
                              : null,
                          }))
                        }
                        min={0}
                        className='input'
                        placeholder={thresholdPlaceholder}
                      />
                      <p
                        style={{
                          color: 'var(--status-warning)',
                          fontSize: '0.7rem',
                          marginTop: '0.35rem',
                          fontWeight: 600,
                        }}>
                        <HiExclamationTriangle
                          style={{ verticalAlign: '-0.125em' }}
                        />{' '}
                        {t('service_modal.labels.threshold_warning')}
                      </p>
                    </div>
                  )}
                  <div style={{ flex: 1 }}>
                    <NumberInput
                      name='critical_threshold'
                      value={
                        formData.critical_threshold === null
                          ? ''
                          : formData.critical_threshold
                      }
                      onChange={(e) =>
                        setFormData((prev) => ({
                          ...prev,
                          critical_threshold: e.target.value
                            ? Number(e.target.value)
                            : null,
                        }))
                      }
                      min={0}
                      className='input'
                      placeholder={thresholdPlaceholder}
                    />
                    <p
                      style={{
                        color: 'var(--status-error)',
                        fontSize: '0.7rem',
                        marginTop: '0.35rem',
                        fontWeight: 600,
                      }}>
                      <HiExclamationTriangle
                        style={{
                          display: 'inline',
                          verticalAlign: 'middle',
                          marginRight: '0.25rem',
                        }}
                      />
                      {t('service_modal.labels.threshold_critical')}
                    </p>
                  </div>
                </div>
                <p
                  style={{
                    color: 'var(--text-tertiary)',
                    fontSize: '0.75rem',
                    marginTop: '0.5rem',
                  }}>
                  {thresholdHint}
                </p>
              </div>
            </div>
          </div>

          {/* Modal Footer */}
          <div className='modal-footer'>
            <button
              type='button'
              onClick={closeModal}
              className='btn btn-secondary'>
              {t('common.cancel')}
            </button>
            <button
              type='submit'
              disabled={submitting || !formData.host_id}
              className='btn btn-primary'
              style={{
                opacity: submitting || !formData.host_id ? 0.5 : 1,
                cursor:
                  submitting || !formData.host_id ? 'not-allowed' : 'pointer',
              }}>
              {submitting
                ? isEditing
                  ? t('service_modal.submit_updating')
                  : t('service_modal.submit_creating')
                : isEditing
                  ? t('service_modal.submit_update')
                  : t('service_modal.submit_create')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
