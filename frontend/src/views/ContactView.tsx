import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt, HiExclamationCircle, HiCheckCircle } from 'react-icons/hi2';
import { contactApi } from '../api';
import { useDocumentMeta } from '../seo/useDocumentMeta';
import './AuthViews.css'; // Reusing the same nice styles

export default function ContactView() {
  const { t } = useTranslation();
  useDocumentMeta('contact');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setSuccess(false);
    setIsLoading(true);

    try {
      await contactApi.send({ name, email, message });
      setSuccess(true);
      setName('');
      setEmail('');
      setMessage('');
    } catch (err: any) {
      setError(err.response?.data?.error || t('contact.error_generic'));
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="auth-container">
      <div className="auth-card" style={{ maxWidth: '500px' }}>
        <div className="auth-header">
          <div className="auth-logo brand-logo-icon brand-logo-icon-xl">
            <HiBolt />
          </div>
          <h1>{t('contact.title')}</h1>
          <p>{t('contact.subtitle')}</p>
        </div>

        {error && (
          <div className="auth-error">
            <HiExclamationCircle />
            <span>{error}</span>
          </div>
        )}

        {success && (
          <div className="auth-error" style={{ backgroundColor: 'rgba(16, 185, 129, 0.1)', color: 'var(--status-success)' }}>
            <HiCheckCircle />
            <span>{t('contact.success')}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="auth-form">
          <div className="form-group">
            <label htmlFor="name">{t('contact.name')}</label>
            <input
              id="name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('contact.name_placeholder')}
              required
            />
          </div>

          <div className="form-group">
            <label htmlFor="email">{t('contact.email')}</label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t('contact.email_placeholder')}
              required
            />
          </div>

          <div className="form-group">
            <label htmlFor="message">{t('contact.message')}</label>
            <textarea
              id="message"
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder={t('contact.message_placeholder')}
              required
              rows={4}
              style={{
                width: '100%',
                padding: '0.75rem 1rem',
                backgroundColor: 'var(--mm-bg-input)',
                border: '1px solid var(--mm-border-color)',
                borderRadius: '8px',
                color: 'var(--mm-text-primary)',
                fontFamily: 'inherit',
                fontSize: '0.95rem',
                resize: 'vertical',
                transition: 'all 0.2s',
              }}
            />
          </div>

          <button type="submit" className="auth-button" disabled={isLoading} style={{ marginTop: '1rem' }}>
            {isLoading ? t('contact.sending') : t('contact.send')}
          </button>
        </form>

        <div className="auth-footer">
          <p>
            <Link to="/">{t('contact.back_home')}</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
