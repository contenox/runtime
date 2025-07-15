import { EmptyState, Panel, Section, Spinner, Table, TableCell, TableRow } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { useKeywords } from '../../../../hooks/useKeywords';

export default function KeywordsSection() {
  const { t } = useTranslation();
  const { data: keywords, isLoading, isError, error } = useKeywords();

  if (isLoading) {
    return (
      <Section className="flex items-center justify-center py-10">
        <Spinner size="lg" />
      </Section>
    );
  }

  if (isError) {
    return (
      <Panel variant="error" title={t('keywords.list_error_title')}>
        {error?.message || t('errors.generic_fetch')}
      </Panel>
    );
  }

  if (!keywords || keywords.length === 0) {
    return (
      <EmptyState
        title={t('keywords.list_empty_title')}
        description={t('keywords.list_empty_message')}
      />
    );
  }

  return (
    <Table columns={[t('common.keyword')]}>
      {keywords.map((keyword, index) => (
        <TableRow key={index}>
          <TableCell>{keyword}</TableCell>
        </TableRow>
      ))}
    </Table>
  );
}
