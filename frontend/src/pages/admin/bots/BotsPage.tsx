import { TabbedPage } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import BotManagementSection from './components/BotManagementSection';

export default function BotPage() {
  const { t } = useTranslation();

  const tabs = [
    {
      id: 'bot-management',
      label: t('bots.manage_title'),
      content: <BotManagementSection />,
    },
  ];

  return <TabbedPage tabs={tabs} />;
}
