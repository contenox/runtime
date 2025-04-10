import { Button, P, Panel, Span } from '@cate/ui';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { AccessEntry } from '../../../../lib/types';

type Props = {
  entries: AccessEntry[];
  onEdit: (entry: AccessEntry) => void;
  onDelete: (id: string) => void;
  deletePending: boolean;
  setSelectedUserSubject: (userSubject: string | undefined) => void;
  selectedUserSubject?: string;
};

const AccessList: React.FC<Props> = ({
  entries,
  onEdit,
  onDelete,
  deletePending,
  setSelectedUserSubject,
  selectedUserSubject,
}) => {
  const { t } = useTranslation();
  const handleClearFilter = () => {
    setSelectedUserSubject(undefined);
  };

  return (
    <>
      {selectedUserSubject && (
        <div className="bg-muted/50 mb-4 flex items-center justify-between rounded-md p-4">
          <Span className="text-muted-foreground text-sm">
            {t('common.filter_by')}
            <P> {selectedUserSubject} </P>
          </Span>

          <Button variant="ghost" size="sm" onClick={handleClearFilter}>
            {t('common.clear_filter')}
          </Button>
        </div>
      )}
      <Panel variant="bordered" className="divide-y">
        {entries.map(entry => (
          <div key={entry.id} className="flex items-center justify-between p-4">
            <div className="space-y-1">
              <P className="text-text text-xs">
                {entry.identityDetails?.friendlyName || entry.identity}
              </P>
              {entry.identityDetails?.email && (
                <P className="text-text-muted text-xs">{entry.identityDetails.email}</P>
              )}
              <P className="text-text-muted text-sm">
                {t('accesscontrol.permission')}: {entry.permission}
              </P>
              <P className="text-text-muted text-xs">
                {t('accesscontrol.resource')}: {entry.resource}
              </P>
            </div>
            <div className="flex gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onEdit(entry)}
                className="text-primary">
                {t('common.edit')}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onDelete(entry.id)}
                className="text-error"
                disabled={deletePending}>
                {deletePending ? t('common.deleting') : t('common.delete')}
              </Button>
            </div>
          </div>
        ))}
      </Panel>
    </>
  );
};

export default AccessList;
