import { TabbedPage } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import TelegramFrontendsSection from './components/TelegramFrontendsSection';

export default function TelegramPage() {
  const { t } = useTranslation();

  const tabs = [
    {
      id: 'telegram-frontends',
      label: t('telegram.manage_title'),
      content: <TelegramFrontendsSection />,
    },
  ];

  return <TabbedPage tabs={tabs} />;
}
