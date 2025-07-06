import { GridLayout, H2, Panel, Section } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import ChainForm from './components/ChainForm';
import ChainsList from './components/ChainsList';

export default function ChainsPage() {
  const { t } = useTranslation();

  return (
    <GridLayout variant="body">
      <Section className="overflow-hidden">
        <H2 className="mb-4">{t('chains.list_title')}</H2>
        <Panel className="overflow-auto">
          <ChainsList />
        </Panel>
      </Section>

      <Section>
        <ChainForm />
      </Section>
    </GridLayout>
  );
}
