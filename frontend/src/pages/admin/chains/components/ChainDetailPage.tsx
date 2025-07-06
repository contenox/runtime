import { GridLayout, H2, Panel, Section, Spinner } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { useChain } from '../../../../hooks/useChains';
import ChainEditor from './ChainEditor';
import ChainPreview from './ChainPreview';

export default function ChainDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { t } = useTranslation();
  const { data: chain, isLoading, error } = useChain(id!);

  if (isLoading) {
    return (
      <Section className="flex justify-center py-10">
        <Spinner size="lg" />
      </Section>
    );
  }

  if (error || !chain) {
    return <Panel variant="error">{error?.message || t('chains.not_found')}</Panel>;
  }

  return (
    <GridLayout variant="body">
      <Section>
        <H2 className="mb-4">{t('chains.editor_title', { id: chain.id })}</H2>
        <ChainEditor chain={chain} />
      </Section>

      <Section>
        <ChainPreview chain={chain} />
      </Section>
    </GridLayout>
  );
}
