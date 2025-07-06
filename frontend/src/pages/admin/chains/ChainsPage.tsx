import { GridLayout, Panel, Section } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import ChainForm from './components/ChainForm';
import ChainsList from './components/ChainsList';

export default function ChainsPage() {
  const { t } = useTranslation();

  return (
    <GridLayout variant="body">
      <Section className="overflow-hidden" title={t('chains.list_title')}>
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
