import { Button, Form, Input, Label, P, Panel, Select, Spinner } from '@cate/ui';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { usePermissions } from '../../../../hooks/useAccess';
import { useSystemServices } from '../../../../hooks/useSystem';
import { AccessEntry } from '../../../../lib/types';

type Props = {
  editingEntry: AccessEntry | null;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
  identity: string;
  setIdentity: (value: string) => void;
  permission: string;
  setPermission: (value: string) => void;
  resource: string;
  setResource: (value: string) => void;
};

const AccessForm: React.FC<Props> = ({
  onCancel,
  onSubmit,
  isPending,
  error,
  identity,
  setIdentity,
  permission,
  setPermission,
  resource,
  setResource,
}) => {
  const { t } = useTranslation();
  const {
    data: permissions,
    isLoading: isPermissionsLoading,
    isError: isPermissionsError,
    error: permissionsError,
  } = usePermissions();
  const { data: services, isLoading, isError, error: servicesError } = useSystemServices();
  if (isLoading || isPermissionsLoading) {
    return <Spinner></Spinner>;
  }
  if (isError) {
    return <Panel variant="error">{servicesError?.message}</Panel>;
  }
  if (isPermissionsError) {
    <Panel variant="error">{permissionsError?.message ?? t('common.error')}</Panel>;
  }

  return (
    <Form onSubmit={onSubmit}>
      <Panel>
        <Label htmlFor="identity">{t('accesscontrol.identity')}</Label>
        <Input
          id="identity"
          value={identity}
          onChange={e => setIdentity(e.target.value)}
          required
        />
      </Panel>
      <Panel>
        <Label htmlFor="permission">{t('accesscontrol.permission')}</Label>
        <Select
          id="permission"
          value={permission}
          onChange={e => setPermission(e.target.value)}
          required
          placeholder={t('accesscontrol.select_permission')}
          options={
            permissions?.map(permission => ({
              value: permission,
              label: permission,
              key: permission,
            })) || []
          }
        />
      </Panel>
      <Panel>
        <Label htmlFor="resource">{t('accesscontrol.resource')}</Label>
        <Select
          id="resource"
          value={resource}
          onChange={e => setResource(e.target.value)}
          required
          placeholder={t('accesscontrol.select_resource')}
          options={
            services?.map(service => ({ value: service, label: service, key: service })) || []
          }
        />
      </Panel>
      {error && <P className="text-error">{t('common.error')}</P>}
      <Panel className="flex gap-2">
        <Button type="submit" variant="primary" disabled={isPending}>
          {isPending ? t('common.saving') : t('common.save')}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t('common.cancel')}
        </Button>
      </Panel>
    </Form>
  );
};

export default AccessForm;
