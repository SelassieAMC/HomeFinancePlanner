import { useTranslation } from 'react-i18next';
import { ProfileTab } from './ProfileTab';

export function ProfilePage() {
  const { t } = useTranslation('settings');
  return (
    <div className="page">
      <h2 className="page-title">{t('profile.pageTitle')}</h2>
      <ProfileTab />
    </div>
  );
}
