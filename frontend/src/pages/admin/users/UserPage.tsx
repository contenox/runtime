import { TabbedPage } from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import AccessControlSection from './AccessControlSection';
import UserSection from './UserSection';

export default function UserPage() {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const [selectedTab, setSelectedTab] = useState(searchParams.get('tab') || 'users');

  const handleTabChange = (tabId: string) => {
    setSelectedTab(tabId);
  };

  const goToAccessControlForUser = (userSubject: string) => {
    setSelectedUserSubject(userSubject);
    setSelectedTab('accesscontrol');
  };

  const selectedUser = searchParams.get('user');
  const [selectedUserSubject, setSelectedUserSubject] = useState<string | undefined>(
    selectedUser ? selectedUser : undefined,
  );
  return (
    <TabbedPage
      tabs={[
        {
          id: 'users',
          label: t('users.manage_title'),
          content: <UserSection goToAccessControlForUser={goToAccessControlForUser} />,
        },
        {
          id: 'accesscontrol',
          label: t('accesscontrol.manage_title'),
          content: (
            <AccessControlSection
              selectedUserSubject={selectedUserSubject}
              setSelectedUserSubject={setSelectedUserSubject}
            />
          ),
        },
      ]}
      defaultActiveTab={selectedTab}
      activeTab={selectedTab}
      onTabChange={handleTabChange}
    />
  );
}
