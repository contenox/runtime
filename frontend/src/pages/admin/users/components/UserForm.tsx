import { Button, Form, Input, Label, P, Panel } from '@contenox/ui';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { User } from '../../../../lib/types';

type UserFormProps = {
  editingUser: User | null;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
  friendlyName: string;
  setFriendlyName: (value: string) => void;
  email: string;
  setEmail: (value: string) => void;
  subject: string;
  setSubject: (value: string) => void;
  password: string;
  setPassword: (value: string) => void;
};

const UserForm: React.FC<UserFormProps> = ({
  editingUser,
  onCancel,
  onSubmit,
  isPending,
  error,
  friendlyName,
  setFriendlyName,
  email,
  setEmail,
  subject,
  setSubject,
  password,
  setPassword,
}) => {
  const { t } = useTranslation();

  return (
    <Form onSubmit={onSubmit}>
      <Panel>
        <Label htmlFor="friendlyName">{t('users.friendlyName')}</Label>
        <Input
          id="friendlyName"
          value={friendlyName}
          onChange={e => setFriendlyName(e.target.value)}
          required
        />
      </Panel>
      <Panel>
        <Label htmlFor="email">{t('users.email')}</Label>
        <Input
          id="email"
          type="email"
          value={email}
          onChange={e => setEmail(e.target.value)}
          required
        />
      </Panel>
      <Panel>
        <Label htmlFor="subject">{t('users.subject')}</Label>
        <Input id="subject" value={subject} onChange={e => setSubject(e.target.value)} required />
      </Panel>
      {/* Only show the password field when creating a new user */}
      {!editingUser && (
        <Panel>
          <Label htmlFor="password">{t('users.password')}</Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            required
          />
        </Panel>
      )}
      {error && <P className="text-error">{t('users.form_error')}</P>}
      <Panel className="flex gap-2">
        <Button type="submit" variant="primary" disabled={isPending}>
          {isPending ? t('users.saving') : t('users.save')}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t('users.cancel')}
        </Button>
      </Panel>
    </Form>
  );
};

export default UserForm;
