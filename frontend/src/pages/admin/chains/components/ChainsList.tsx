import {
  Button,
  EmptyState,
  Panel,
  Section,
  Spinner,
  Table,
  TableCell,
  TableRow,
} from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { useChains, useDeleteChain } from '../../../../hooks/useChains';
import ChainStatusBadge from './ChainStatusBadge';

export default function ChainsList() {
  const { t } = useTranslation();
  const { data: chains, isLoading, error } = useChains();
  const deleteChain = useDeleteChain();
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const handleDelete = async (id: string) => {
    setDeletingId(id);
    try {
      await deleteChain.mutateAsync(id);
    } finally {
      setDeletingId(null);
    }
  };

  if (isLoading) {
    return (
      <Section className="flex justify-center py-10">
        <Spinner size="lg" />
      </Section>
    );
  }

  if (error) {
    return <Panel variant="error">{t('chains.list_error')}</Panel>;
  }

  if (!chains || chains.length === 0) {
    return (
      <EmptyState
        title={t('chains.list_empty_title')}
        description={t('chains.list_empty_message')}
      />
    );
  }

  return (
    <Table
      columns={[t('chains.id'), t('chains.description'), t('chains.status'), t('common.actions')]}>
      {chains.map(chain => {
        const isDeleting = deletingId === chain.id;
        const isDefault = chain.id === 'openai_chat_chain' || chain.id === 'chat_chain';

        return (
          <TableRow key={chain.id}>
            <TableCell className="font-mono">{chain.id}</TableCell>
            <TableCell>{chain.description}</TableCell>
            <TableCell>
              <ChainStatusBadge isDefault={isDefault} />
            </TableCell>
            <TableCell className="space-x-2">
              <Link to={`/chains/${chain.id}`}>
                <Button variant="accent" size="sm">
                  {t('common.edit')}
                </Button>
              </Link>
              {!isDefault && (
                <Button
                  variant="accent"
                  size="sm"
                  onClick={() => handleDelete(chain.id)}
                  disabled={isDeleting}>
                  {isDeleting ? <Spinner size="sm" /> : t('common.delete')}
                </Button>
              )}
            </TableCell>
          </TableRow>
        );
      })}
    </Table>
  );
}
