import { useTranslation } from 'react-i18next';
import { HiOutlineWrenchScrewdriver } from 'react-icons/hi2';
import { IconType } from 'react-icons';
import './PlaceholderView.css';

interface PlaceholderViewProps {
  title: string;
  description: string;
  icon?: IconType;
}

function PlaceholderView({ title, description, icon: Icon = HiOutlineWrenchScrewdriver }: PlaceholderViewProps) {
  const { t } = useTranslation();

  return (
    <div className="placeholder-view">
      <div className="placeholder-content">
        <div className="placeholder-icon">
          <Icon />
        </div>
        <h1 className="placeholder-title">{title}</h1>
        <p className="placeholder-description">{description}</p>
        <div className="placeholder-badge">{t('common.coming_soon')}</div>
      </div>
    </div>
  );
}

export default PlaceholderView;
