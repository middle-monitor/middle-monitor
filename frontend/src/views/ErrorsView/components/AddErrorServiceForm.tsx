import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { HiPlus, HiX } from 'react-icons/hi';
import {
  HiOutlineClipboard,
  HiOutlineCheckCircle,
  HiOutlineGlobeAlt,
} from 'react-icons/hi2';
import { SiGo, SiPython, SiRust, SiTypescript } from 'react-icons/si';
import type { IconType } from 'react-icons';
import { PUBLIC_API_URL, type Host } from '../../../api';
import { useOrgApi } from '../../../hooks/useOrgApi';

interface Platform {
  id: string;
  name: string;
  icon: IconType;
  color: string;
}

const PLATFORMS: Platform[] = [
  {
    id: 'go',
    name: 'Go',
    icon: SiGo,
    color: '#00ADD8',
  },
  {
    id: 'python',
    name: 'Python',
    icon: SiPython,
    color: '#3776AB',
  },
  {
    id: 'rust',
    name: 'Rust',
    icon: SiRust,
    color: '#DEA584',
  },
  {
    id: 'typescript',
    name: 'TypeScript',
    icon: SiTypescript,
    color: '#3178C6',
  },
  {
    id: 'web',
    name: 'Web',
    icon: HiOutlineGlobeAlt,
    color: '#38BDF8',
  },
];

interface SdkSnippet {
  install: string;
  init: string;
}

// Mirrors each SDK's real InitWithConfig/init_with_config API (see sdks/<lang>/README.md).
function getSdkSnippet(
  platform: string,
  service: string,
  token: string,
  rustComment: string
): SdkSnippet {
  switch (platform) {
    case 'go':
      return {
        install: 'go get github.com/middle-monitor/sdk-go',
        init: `import "github.com/middle-monitor/sdk-go"

func main() {
    middlemonitor.InitWithConfig(
        "${PUBLIC_API_URL}",
        "${service}",
        "${token}",
    )
}`,
      };
    case 'python':
      return {
        install:
          'pip install middle-monitor-sdk',
        init: `from middlemonitor import init_with_config

init_with_config(
    api_url="${PUBLIC_API_URL}",
    service="${service}",
    token="${token}",
)`,
      };
    case 'rust':
      return {
        install: `${rustComment}
middle-monitor-sdk = { git = "https://github.com/middle-monitor/sdk-rust.git" }`,
        init: `use middle_monitor_sdk::init_with_config;

init_with_config(
    "${PUBLIC_API_URL}".to_string(),
    "${service}".to_string(),
    Some("${token}".to_string()),
);`,
      };
    case 'typescript':
      return {
        install:
          'npm install @middle-monitor/sdk',
        init: `import { initWithConfig } from '@middle-monitor/sdk';

initWithConfig(
  '${PUBLIC_API_URL}',
  '${service}',
  '${token}',
);`,
      };
    case 'web':
      return {
        install: 'npm install @middle-monitor/web',
        init: `import { init } from '@middle-monitor/web';

init({
  apiUrl: '${PUBLIC_API_URL}',
  service: '${service}',
  token: '${token}',
});`,
      };
    default:
      return { install: '', init: '' };
  }
}

interface AddErrorServiceFormProps {
  onSuccess: () => void;
  onCancel: () => void;
}

export function AddErrorServiceForm({
  onSuccess,
  onCancel,
}: AddErrorServiceFormProps) {
  const { t } = useTranslation();
  const orgApi = useOrgApi();
  const [hosts, setHosts] = useState<Host[]>([]);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [copiedField, setCopiedField] = useState<
    'token' | 'install' | 'init' | null
  >(null);
  const [formData, setFormData] = useState({
    platform: '',
    name: '',
    hostId: '',
  });

  useEffect(() => {
    fetchHosts();
    // Hosts are read once when the form mounts; fetchHosts is redeclared on
    // every render, so depending on it would refetch on each keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const fetchHosts = async () => {
    try {
      setLoading(true);
      const response = await orgApi.hosts.list();
      setHosts(response.data || []);
    } catch (err) {
      console.error('Failed to fetch hosts:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);

    if (!formData.platform || !formData.name || !formData.hostId) {
      setError(t('errors_view.add_service_form.fill_required'));
      setSubmitting(false);
      return;
    }

    try {
      const selectedHost = hosts.find(
        (h) => h.id === parseInt(formData.hostId)
      );
      if (!selectedHost) {
        setError(t('errors_view.add_service_form.host_not_found'));
        setSubmitting(false);
        return;
      }

      const response = await orgApi.services.create({
        host_id: parseInt(formData.hostId),
        name: formData.name,
        type: `error_service_${formData.platform}`,
        host: selectedHost.host,
        service: selectedHost.service,
        service_interval: 0,
        max_attempts: 0,
      });

      if (response.data.token) {
        setCreatedToken(response.data.token);
      } else {
        onSuccess();
      }
    } catch (err: any) {
      setError(
        err.response?.data?.error ||
          t('errors_view.add_service_form.create_error')
      );
      console.error(err);
    } finally {
      setSubmitting(false);
    }
  };

  const handleChange = (
    e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>
  ) => {
    const { name, value } = e.target;
    setFormData((prev) => ({ ...prev, [name]: value }));
  };

  const selectedPlatform = PLATFORMS.find((p) => p.id === formData.platform);
  const snippet =
    createdToken && selectedPlatform
      ? getSdkSnippet(
          selectedPlatform.id,
          formData.name,
          createdToken,
          t('errors_view.add_service_form.install_rust_comment')
        )
      : null;

  const copyToClipboard = (text: string, field: 'token' | 'install' | 'init') => {
    navigator.clipboard.writeText(text);
    setCopiedField(field);
    setTimeout(() => setCopiedField(null), 2000);
  };

  return (
    <div
      className='card'
      style={{
        marginBottom: '1rem',
        border: '1px solid var(--status-error-border)',
      }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: '1.25rem',
        }}>
        <h3
          style={{
            color: 'var(--text-primary)',
            fontSize: '1rem',
            fontWeight: 600,
            display: 'flex',
            alignItems: 'center',
            gap: '0.5rem',
            margin: 0,
          }}>
          <HiPlus style={{ fontSize: '1rem', color: 'var(--status-error)' }} />
          <span>{t('errors_view.add_service_form.title')}</span>
        </h3>
        <button
          type='button'
          onClick={onCancel}
          className='btn btn-ghost'
          style={{ padding: '0.25rem' }}>
          <HiX style={{ fontSize: '1.25rem' }} />
        </button>
      </div>

      {error && <div className='error-message' style={{ marginBottom: '1rem' }}>{error}</div>}

      <form onSubmit={handleSubmit}>
        {/* Platform Selection */}
        <div style={{ marginBottom: '1.5rem' }}>
          <label className='label'>{t('errors_view.add_service_form.platform')}</label>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(5, 1fr)',
              gap: '0.75rem',
            }}>
            {PLATFORMS.map((platform) => {
              const PlatformIcon = platform.icon;
              return (
                <button
                  key={platform.id}
                  type='button'
                  onClick={() =>
                    setFormData((prev) => ({ ...prev, platform: platform.id }))
                  }
                  style={{
                    padding: '1rem',
                    background:
                      formData.platform === platform.id
                        ? 'var(--brand-primary-light)'
                        : 'var(--bg-secondary)',
                    border:
                      formData.platform === platform.id
                        ? `2px solid ${platform.color}`
                        : '1px solid var(--border-primary)',
                    borderRadius: '8px',
                    cursor: 'pointer',
                    transition: 'all 0.15s ease',
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    gap: '0.5rem',
                  }}>
                  <PlatformIcon style={{ fontSize: '1.5rem', color: platform.color }} />
                  <span
                    style={{
                      color:
                        formData.platform === platform.id
                          ? 'var(--text-primary)'
                          : 'var(--text-secondary)',
                      fontSize: '0.875rem',
                      fontWeight: formData.platform === platform.id ? 600 : 400,
                    }}>
                    {platform.name}
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        {/* Application Name */}
        <div style={{ marginBottom: '1.5rem' }}>
          <label className='label'>{t('errors_view.add_service_form.service_name')}</label>
          <input
            type='text'
            name='name'
            value={formData.name}
            onChange={handleChange}
            required
            className='input'
            placeholder={t(
              formData.platform === 'web'
                ? 'errors_view.add_service_form.service_name_placeholder_web'
                : 'errors_view.add_service_form.service_name_placeholder'
            )}
          />
          <p style={{ margin: '0.375rem 0 0', fontSize: '0.75rem', color: 'var(--text-tertiary)' }}>
            {t('errors_view.add_service_form.name_hint')}
          </p>
        </div>

        {/* Host Selection */}
        <div style={{ marginBottom: '1.5rem' }}>
          <label className='label'>{t('errors_view.add_service_form.host')}</label>
          {loading ? (
            <div style={{ color: 'var(--text-tertiary)', fontSize: '0.875rem' }}>
              {t('errors_view.add_service_form.loading_hosts')}
            </div>
          ) : hosts.length === 0 ? (
            <div className='error-message'>
              {t('errors_view.add_service_form.no_hosts')}
            </div>
          ) : (
            <select
              name='hostId'
              value={formData.hostId}
              onChange={handleChange}
              required
              className='select'>
              <option value=''>{t('errors_view.add_service_form.select_host')}</option>
              {hosts.map((host) => (
                <option key={host.id} value={host.id}>
                  {host.name} ({host.host}) - {host.service}
                </option>
              ))}
            </select>
          )}
        </div>

        {/* Token Display */}
        {createdToken && (
          <div
            style={{
              marginBottom: '1.5rem',
              padding: '1rem',
              background: 'var(--status-success-bg)',
              border: '1px solid var(--status-success-border)',
              borderRadius: '8px',
            }}>
            <div
              style={{
                color: 'var(--status-success)',
                fontSize: '0.875rem',
                fontWeight: 600,
                marginBottom: '0.75rem',
              }}>
              {t('errors_view.add_service_form.created_success')}
            </div>
            <div style={{ marginBottom: '0.5rem' }}>
              <label className='label'>
                {t('errors_view.add_service_form.your_token')}
              </label>
              <div
                style={{
                  display: 'flex',
                  gap: '0.5rem',
                  alignItems: 'center',
                }}>
                <code
                  style={{
                    flex: 1,
                    padding: '0.75rem',
                    background: 'var(--bg-tertiary)',
                    border: '1px solid var(--status-success-border)',
                    borderRadius: '6px',
                    color: 'var(--status-success)',
                    fontSize: '0.75rem',
                    fontFamily: 'monospace',
                    wordBreak: 'break-all',
                  }}>
                  {createdToken}
                </code>
                <button
                  type='button'
                  onClick={() => copyToClipboard(createdToken, 'token')}
                  className='btn btn-secondary'
                  style={{ flexShrink: 0 }}>
                  {copiedField === 'token' ? (
                    <>
                      <HiOutlineCheckCircle /> {t('common.copied')}
                    </>
                  ) : (
                    <>
                      <HiOutlineClipboard /> {t('common.copy')}
                    </>
                  )}
                </button>
              </div>
            </div>
            <div
              style={{
                marginTop: '1rem',
                padding: '0.75rem',
                background: 'var(--status-info-bg)',
                border: '1px solid var(--status-info-border)',
                borderRadius: '6px',
                color: 'var(--status-info)',
                fontSize: '0.75rem',
              }}>
              <strong>{t('errors_view.add_service_form.token_important')}</strong>{' '}
              {t('errors_view.add_service_form.token_once')}
            </div>
          </div>
        )}

        {/* Get started: shown once the application (and its token) exists */}
        {snippet && (
          <div
            style={{
              marginBottom: '1.5rem',
              padding: '1rem',
              background: 'var(--status-info-bg)',
              border: '1px solid var(--status-info-border)',
              borderRadius: '8px',
            }}>
            <div
              style={{
                color: 'var(--status-info)',
                fontSize: '0.875rem',
                fontWeight: 600,
                marginBottom: '0.75rem',
              }}>
              {t('errors_view.add_service_form.install_instructions')}
            </div>

            <div style={{ marginBottom: '0.375rem' }}>
              <strong style={{ fontSize: '0.75rem', color: 'var(--text-secondary)' }}>
                {t('errors_view.add_service_form.install_step1')}
              </strong>
            </div>
            <div
              style={{
                display: 'flex',
                gap: '0.5rem',
                alignItems: 'flex-start',
                marginBottom: '1rem',
              }}>
              <code
                style={{
                  flex: 1,
                  display: 'block',
                  padding: '0.75rem',
                  background: 'var(--bg-tertiary)',
                  borderRadius: '6px',
                  fontSize: '0.75rem',
                  color: 'var(--text-primary)',
                  whiteSpace: 'pre',
                  overflowX: 'auto',
                }}>
                {snippet.install}
              </code>
              <button
                type='button'
                onClick={() => copyToClipboard(snippet.install, 'install')}
                className='btn btn-ghost'
                style={{ flexShrink: 0 }}>
                {copiedField === 'install' ? (
                  <HiOutlineCheckCircle />
                ) : (
                  <HiOutlineClipboard />
                )}
              </button>
            </div>

            <div style={{ marginBottom: '0.375rem' }}>
              <strong style={{ fontSize: '0.75rem', color: 'var(--text-secondary)' }}>
                {t('errors_view.add_service_form.install_step2')}
              </strong>
            </div>
            <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'flex-start' }}>
              <code
                style={{
                  flex: 1,
                  display: 'block',
                  padding: '0.75rem',
                  background: 'var(--bg-tertiary)',
                  borderRadius: '6px',
                  fontSize: '0.75rem',
                  color: 'var(--text-primary)',
                  whiteSpace: 'pre',
                  overflowX: 'auto',
                }}>
                {snippet.init}
              </code>
              <button
                type='button'
                onClick={() => copyToClipboard(snippet.init, 'init')}
                className='btn btn-ghost'
                style={{ flexShrink: 0 }}>
                {copiedField === 'init' ? (
                  <HiOutlineCheckCircle />
                ) : (
                  <HiOutlineClipboard />
                )}
              </button>
            </div>
          </div>
        )}

        {/* Actions */}
        <div
          style={{ display: 'flex', gap: '0.75rem', justifyContent: 'flex-end' }}>
          <button type='button' onClick={onCancel} className='btn btn-secondary'>
            {t('common.cancel')}
          </button>
          {createdToken ? (
            <button
              type='button'
              onClick={() => {
                setCreatedToken(null);
                setFormData({
                  platform: '',
                  name: '',
                  hostId: '',
                });
                onSuccess();
              }}
              className='btn btn-primary'>
              {t('common.close')}
            </button>
          ) : (
            <button
              type='submit'
              disabled={
                submitting ||
                !formData.platform ||
                !formData.name ||
                !formData.hostId
              }
              className='btn btn-primary'
              style={{
                opacity:
                  submitting ||
                  !formData.platform ||
                  !formData.name ||
                  !formData.hostId
                    ? 0.5
                    : 1,
                cursor:
                  submitting ||
                  !formData.platform ||
                  !formData.name ||
                  !formData.hostId
                    ? 'not-allowed'
                    : 'pointer',
              }}>
              {submitting ? t('errors_view.add_service_form.creating') : t('errors_view.add_service_form.create_service')}
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
