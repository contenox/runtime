import { GridLayout, Panel, Section } from '@contenox/ui';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCreateUser, useDeleteUser, useUpdateUser, useUsers } from '../../../hooks/useUsers';
import { User } from '../../../lib/types';
import UserForm from './components/UserForm';
import UserList from './components/UserList';

type UserSectionProps = {
  goToAccessControlForUser: (userSubject: string) => void;
};

export default function UserSection({ goToAccessControlForUser }: UserSectionProps) {
  const { t } = useTranslation();
  const { data: users, isLoading, error } = useUsers();
  const createUserMutation = useCreateUser();
  const updateUserMutation = useUpdateUser();
  const deleteUserMutation = useDeleteUser();

  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [friendlyName, setFriendlyName] = useState('');
  const [email, setEmail] = useState('');
  const [subject, setSubject] = useState('');
  const [password, setPassword] = useState('');

  const resetForm = () => {
    setFriendlyName('');
    setEmail('');
    setSubject('');
    setPassword('');
    setEditingUser(null);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (editingUser) {
      updateUserMutation.mutate(
        {
          id: editingUser.id,
          data: { friendlyName, email, subject, ...(password && { password }) },
        },
        { onSuccess: resetForm },
      );
    } else {
      createUserMutation.mutate(
        { friendlyName, email, subject, password },
        { onSuccess: resetForm },
      );
    }
  };

  const handleEdit = (user: User) => {
    setEditingUser(user);
    setFriendlyName(user.friendlyName || '');
    setEmail(user.email);
    setSubject(user.subject);
  };

  const handleDelete = (id: string) => {
    deleteUserMutation.mutate(id);
  };

  return (
    <>
      <GridLayout variant="body">
        <Section>
          {isLoading && <Panel>{t('users.list_loading')}</Panel>}
          {error && <Panel variant="error">{t('users.list_error')}</Panel>}
          {users && users.length > 0 ? (
            <UserList
              users={users}
              onEdit={handleEdit}
              onDelete={handleDelete}
              deletePending={deleteUserMutation.isPending}
              goToAccessControlForUser={goToAccessControlForUser}
            />
          ) : (
            <Panel variant="error">{t('users.list_404')}</Panel>
          )}
        </Section>
        <Section>
          <UserForm
            editingUser={editingUser}
            onCancel={resetForm}
            onSubmit={handleSubmit}
            isPending={editingUser ? updateUserMutation.isPending : createUserMutation.isPending}
            error={createUserMutation.isError || updateUserMutation.isError}
            friendlyName={friendlyName}
            setFriendlyName={setFriendlyName}
            email={email}
            setEmail={setEmail}
            subject={subject}
            setSubject={setSubject}
            password={password}
            setPassword={setPassword}
          />
        </Section>
      </GridLayout>
    </>
  );
}
