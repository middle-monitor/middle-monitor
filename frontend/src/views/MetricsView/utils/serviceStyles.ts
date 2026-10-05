export const getServiceStatusStyles = (status: string) => {
  const styles = {
    background: '',
    border: '',
  };

  switch (status) {
    case 'failure':
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--status-error-border)';
      break;
    case 'warning':
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--status-warning-border)';
      break;
    case 'success':
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--status-success-border)';
      break;
    default:
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--border-primary)';
  }

  return styles;
};

export const getHostStatusStyles = (status: string) => {
  const styles = {
    background: '',
    border: '',
  };

  switch (status) {
    case 'failure':
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--status-error-border)';
      break;
    case 'success':
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--status-success-border)';
      break;
    default:
      styles.background = 'var(--surface-primary)';
      styles.border = 'var(--border-primary)';
  }

  return styles;
};
