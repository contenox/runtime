import { Button, P, Section } from '@contenox/ui';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { User } from '../../../../lib/types';

type UserListProps = {
  users: User[];
  onEdit: (user: User) => void;
  onDelete: (id: string) => void;
  deletePending: boolean;
  goToAccessControlForUser: (userSubject: string) => void;
};

const UserList: React.FC<UserListProps> = ({
  users,
  onEdit,
  onDelete,
  deletePending,
  goToAccessControlForUser,
}) => {
  const { t } = useTranslation();

  return (
    <>
      {users.map(user => (
        <Section key={user.id} title={user.friendlyName || user.email}>
          <div>
            <P>{user.email}</P>
            <P>
              {t('users.subject')}: {user.subject}
            </P>
          </div>
          <Button variant="ghost" size="sm" onClick={() => onEdit(user)} className="text-primary">
            {t('common.edit')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onDelete(user.id)}
            className="text-error"
            disabled={deletePending}>
            {deletePending ? t('common.deleting') : t('common.delete')}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => goToAccessControlForUser(user.subject)}>
            {t('accesscontrol.manage_title')}
          </Button>
        </Section>
      ))}
    </>
  );
};

export default UserList;
