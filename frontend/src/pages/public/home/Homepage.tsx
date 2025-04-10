// HomePage.tsx
import { P } from '@cate/ui';
import React from 'react';
import { useTranslation } from 'react-i18next';

const HomePage: React.FC = () => {
  const { t } = useTranslation();
  return <P>{t('pages.welcome_title')}</P>;
};

export default HomePage;
