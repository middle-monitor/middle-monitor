import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  HiBolt,
  HiExclamationCircle,
  HiOutlineServerStack,
  HiOutlineCube,
  HiOutlineChartBar,
  HiOutlineExclamationTriangle,
  HiCheck,
  HiXMark,
} from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import './AuthViews.css';
import './CreateOrganizationView.css';

const EMAIL_RE = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

function PasswordStrength({ password }: { password: string }) {
  const { t } = useTranslation();
  const rules = [
    { label: t('auth.create_org.pw_rule_length'), ok: password.length >= 8 },
    { label: t('auth.create_org.pw_rule_upper'), ok: /[A-Z]/.test(password) },
    { label: t('auth.create_org.pw_rule_lower'), ok: /[a-z]/.test(password) },
    { label: t('auth.create_org.pw_rule_number'), ok: /[0-9]/.test(password) },
  ];

  if (!password) return null;

  return (
    <ul className="password-rules">
      {rules.map((r) => (
        <li key={r.label} className={r.ok ? 'rule-ok' : 'rule-fail'}>
          {r.ok ? <HiCheck /> : <HiXMark />}
          <span>{r.label}</span>
        </li>
      ))}
    </ul>
  );
}

export default function CreateOrganizationView() {
  const { t } = useTranslation();
  const [organizationName, setOrganizationName] = useState('');
  const [organizationSlug, setOrganizationSlug] = useState('');
  const [slugEdited, setSlugEdited] = useState(false);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [submitError, setSubmitError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const { register } = useAuth();
  const navigate = useNavigate();

  const FREE_PLAN_FEATURES = [
    { icon: HiOutlineServerStack, text: t('auth.create_org.feature_hosts') },
    { icon: HiOutlineChartBar, text: t('auth.create_org.feature_metrics') },
    { icon: HiOutlineCube, text: t('auth.create_org.feature_services') },
    { icon: HiOutlineExclamationTriangle, text: t('auth.create_org.feature_sdk') },
  ];

  const validateField = (field: string, value: string): string => {
    switch (field) {
      case 'organizationName':
        if (!value.trim()) return t('auth.create_org.org_name') + ' ' + t('common.required');
        if (value.length > 255) return t('metrics_view.add_host.error_name_too_long');
        return '';
      case 'organizationSlug':
        if (!value.trim()) return t('auth.create_org.slug') + ' ' + t('common.required');
        if (!SLUG_RE.test(value)) return t('auth.create_org.error_slug_format');
        return '';
      case 'name':
        if (!value.trim()) return t('auth.create_org.your_name') + ' ' + t('common.required');
        return '';
      case 'email':
        if (!EMAIL_RE.test(value)) return t('settings.modal.error_email_invalid');
        return '';
      case 'confirmPassword':
        if (value !== password) return t('auth.create_org.error_password_mismatch');
        return '';
      default:
        return '';
    }
  };

  const handleBlur = (field: string, value: string) => {
    const err = validateField(field, value);
    setFieldErrors((prev) => ({ ...prev, [field]: err }));
  };

  const clearFieldError = (field: string) => {
    if (fieldErrors[field]) {
      setFieldErrors((prev) => ({ ...prev, [field]: '' }));
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError('');

    const errors = {
      organizationName: validateField('organizationName', organizationName),
      organizationSlug: validateField('organizationSlug', organizationSlug),
      name: validateField('name', name),
      email: validateField('email', email),
      confirmPassword: validateField('confirmPassword', confirmPassword),
    };
    setFieldErrors(errors);
    if (Object.values(errors).some(Boolean)) return;

    if (password.length < 8) {
      setSubmitError(t('auth.create_org.error_password_short'));
      return;
    }

    setIsLoading(true);
    try {
      const result = await register(email, password, name, organizationName, organizationSlug);
      const slug = result?.organization?.slug ?? 'default';
      navigate(`/organizations/${slug}`);
    } catch (err: any) {
      setSubmitError(err.response?.data?.error || t('auth.create_org.error_generic'));
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="create-org-page">
      <div className="auth-lang-switch">
        <LanguageSwitcher variant="compact" />
      </div>
      <div className="create-org-layout">
        <aside className="create-org-sidebar">
          <Link to="/" className="create-org-logo">
            <div className="brand-logo-icon brand-logo-icon-lg">
              <HiBolt />
            </div>
            <span>Middle Monitor</span>
          </Link>
          <div className="create-org-free-badge">{t('auth.create_org.free_badge')}</div>
          <h2>{t('auth.create_org.plan_title')}</h2>
          <p>{t('auth.create_org.plan_desc')}</p>
          <ul className="create-org-features">
            {FREE_PLAN_FEATURES.map((f, i) => (
              <li key={i}>
                <f.icon className="create-org-feature-icon" />
                <span>{f.text}</span>
              </li>
            ))}
          </ul>
        </aside>

        <main className="create-org-main">
          <div className="auth-card create-org-card">
            <div className="auth-header">
              <div className="auth-logo brand-logo-icon brand-logo-icon-xl">
                <HiBolt />
              </div>
              <h1>{t('auth.create_org.title')}</h1>
              <p>{t('auth.create_org.subtitle')}</p>
            </div>

            {submitError && (
              <div className="auth-error">
                <HiExclamationCircle />
                <span>{submitError}</span>
              </div>
            )}

            <form onSubmit={handleSubmit} className="auth-form" noValidate>
              <div className="form-group">
                <label htmlFor="organizationName">{t('auth.create_org.org_name')}</label>
                <input
                  id="organizationName"
                  type="text"
                  value={organizationName}
                  onChange={(e) => {
                    setOrganizationName(e.target.value);
                    clearFieldError('organizationName');
                    if (!slugEdited) {
                      setOrganizationSlug(slugify(e.target.value));
                      clearFieldError('organizationSlug');
                    }
                  }}
                  onBlur={(e) => handleBlur('organizationName', e.target.value)}
                  placeholder={t('auth.create_org.org_name_placeholder')}
                  className={fieldErrors.organizationName ? 'input-invalid' : ''}
                />
                {fieldErrors.organizationName && <p className="field-error">{fieldErrors.organizationName}</p>}
              </div>

              <div className="form-group">
                <label htmlFor="organizationSlug">{t('auth.create_org.slug')}</label>
                <input
                  id="organizationSlug"
                  type="text"
                  value={organizationSlug}
                  onChange={(e) => {
                    setSlugEdited(true);
                    setOrganizationSlug(e.target.value);
                    clearFieldError('organizationSlug');
                  }}
                  onBlur={(e) => handleBlur('organizationSlug', e.target.value)}
                  placeholder={t('auth.create_org.slug_placeholder')}
                  className={fieldErrors.organizationSlug ? 'input-invalid' : ''}
                />
                {fieldErrors.organizationSlug ? (
                  <p className="field-error">{fieldErrors.organizationSlug}</p>
                ) : (
                  organizationSlug && (
                    <p className="password-hint">
                      {t('auth.create_org.slug_preview')}: /organizations/{organizationSlug}
                    </p>
                  )
                )}
              </div>

              <div className="form-group">
                <label htmlFor="name">{t('auth.create_org.your_name')}</label>
                <input
                  id="name"
                  type="text"
                  value={name}
                  onChange={(e) => { setName(e.target.value); clearFieldError('name'); }}
                  onBlur={(e) => handleBlur('name', e.target.value)}
                  placeholder={t('auth.create_org.name_placeholder')}
                  autoComplete="name"
                  className={fieldErrors.name ? 'input-invalid' : ''}
                />
                {fieldErrors.name && <p className="field-error">{fieldErrors.name}</p>}
              </div>

              <div className="form-group">
                <label htmlFor="email">{t('auth.create_org.email')}</label>
                <input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => { setEmail(e.target.value); clearFieldError('email'); }}
                  onBlur={(e) => handleBlur('email', e.target.value)}
                  placeholder={t('auth.create_org.email_placeholder')}
                  autoComplete="email"
                  className={fieldErrors.email ? 'input-invalid' : ''}
                />
                {fieldErrors.email && <p className="field-error">{fieldErrors.email}</p>}
              </div>

              <div className="form-row">
                <div className="form-group">
                  <label htmlFor="password">{t('auth.create_org.password')}</label>
                  <input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(e) => { setPassword(e.target.value); clearFieldError('confirmPassword'); }}
                    placeholder="••••••••"
                    autoComplete="new-password"
                  />
                </div>
                <div className="form-group">
                  <label htmlFor="confirmPassword">{t('auth.create_org.confirm')}</label>
                  <input
                    id="confirmPassword"
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => { setConfirmPassword(e.target.value); clearFieldError('confirmPassword'); }}
                    onBlur={(e) => handleBlur('confirmPassword', e.target.value)}
                    placeholder="••••••••"
                    autoComplete="new-password"
                    className={fieldErrors.confirmPassword ? 'input-invalid' : ''}
                  />
                  {fieldErrors.confirmPassword && <p className="field-error">{fieldErrors.confirmPassword}</p>}
                </div>
              </div>

              <PasswordStrength password={password} />

              <button type="submit" className="auth-button" disabled={isLoading}>
                {isLoading ? t('auth.create_org.submitting') : t('auth.create_org.submit')}
              </button>
            </form>

            <div className="auth-footer">
              <p>
                {t('auth.create_org.have_account')} <Link to="/login">{t('auth.create_org.sign_in')}</Link>
              </p>
            </div>
          </div>
        </main>
      </div>
    </div>
  );
}
