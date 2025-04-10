import { GridLayout, Panel, Section } from '@cate/ui';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAccessEntries,
  useCreateAccessEntry,
  useDeleteAccessEntry,
  useUpdateAccessEntry,
} from '../../../hooks/useAccess';
import { AccessEntry } from '../../../lib/types';
import AccessForm from './components/AccessForm';
import AccessList from './components/AccessList';

type AccessControlSectionProps = {
  selectedUserSubject?: string;
  setSelectedUserSubject: (userSubject: string | undefined) => void;
};

export default function AccessControlSection({
  selectedUserSubject,
  setSelectedUserSubject,
}: AccessControlSectionProps) {
  const { t } = useTranslation();
  const { data: entries, isLoading, error } = useAccessEntries(true, selectedUserSubject);

  const createEntry = useCreateAccessEntry();
  const updateEntry = useUpdateAccessEntry();
  const deleteEntry = useDeleteAccessEntry();

  const [editingEntry, setEditingEntry] = useState<AccessEntry | null>(null);
  const [identity, setIdentity] = useState('');
  const [permission, setPermission] = useState('');
  const [resource, setResource] = useState('');

  const resetForm = () => {
    setEditingEntry(null);
    setIdentity('');
    setPermission('');
    setResource('');
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const payload = { identity, permission, resource };
    if (editingEntry) {
      updateEntry.mutate({ id: editingEntry.id, data: payload }, { onSuccess: resetForm });
    } else {
      createEntry.mutate(payload, { onSuccess: resetForm });
    }
  };

  const handleEdit = (entry: AccessEntry) => {
    setEditingEntry(entry);
    setIdentity(entry.identity);
    setPermission(entry.permission);
    setResource(entry.resource);
  };

  const handleDelete = (id: string) => {
    deleteEntry.mutate(id);
  };

  return (
    <GridLayout variant="body">
      <Section>
        {isLoading && <Panel>{t('common.loading')}</Panel>}
        {error && <Panel variant="error">{t('common.error')}</Panel>}
        {entries && entries.length > 0 ? (
          <AccessList
            entries={entries}
            onEdit={handleEdit}
            onDelete={handleDelete}
            deletePending={deleteEntry.isPending}
            setSelectedUserSubject={setSelectedUserSubject}
            selectedUserSubject={selectedUserSubject}
          />
        ) : (
          <Panel variant="error">{t('accesscontrol.list_404')}</Panel>
        )}
      </Section>
      <Section>
        <AccessForm
          editingEntry={editingEntry}
          onCancel={resetForm}
          onSubmit={handleSubmit}
          isPending={editingEntry ? updateEntry.isPending : createEntry.isPending}
          error={createEntry.isError || updateEntry.isError}
          identity={identity}
          setIdentity={setIdentity}
          permission={permission}
          setPermission={setPermission}
          resource={resource}
          setResource={setResource}
        />
      </Section>
    </GridLayout>
  );
}
