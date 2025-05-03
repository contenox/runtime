import {
  Button,
  Form,
  FormField,
  GridLayout,
  Input,
  Panel,
  Section,
  Span,
  Spinner,
} from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearch } from '../../../hooks/useSearch';

export default function SearchPage() {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [topk, setTopk] = useState<number>();
  const [radius, setRadius] = useState<number>();
  const [epsilon, setEpsilon] = useState<number>();
  const [searchParams, setSearchParams] = useState<{
    query: string;
    topk?: number;
    radius?: number;
    epsilon?: number;
  }>();

  const { data, isError, error, isPending } = useSearch(
    searchParams?.query || '',
    searchParams?.topk,
    searchParams?.radius,
    searchParams?.epsilon,
  );

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setSearchParams({ query, topk, radius, epsilon });
  };

  return (
    <GridLayout variant="body">
      <Section>
        <Form
          onSubmit={handleSubmit}
          title={t('search.title')}
          actions={
            <Button type="submit" variant="primary">
              {t('search.search')}
            </Button>
          }>
          <FormField label={t('search.query')} required>
            <Input
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder={t('search.query_placeholder')}
            />
          </FormField>

          <FormField label={t('search.topk')}>
            <Input
              type="number"
              value={topk?.toString() ?? ''}
              onChange={e => setTopk(e.target.value ? parseInt(e.target.value) : undefined)}
              placeholder={t('search.topk_placeholder')}
            />
          </FormField>

          <FormField label={t('search.radius')}>
            <Input
              type="number"
              step="0.01"
              value={radius?.toString() ?? ''}
              onChange={e => setRadius(e.target.value ? parseFloat(e.target.value) : undefined)}
              placeholder={t('search.radius_placeholder')}
            />
          </FormField>

          <FormField label={t('search.epsilon')}>
            <Input
              type="number"
              step="0.01"
              value={epsilon?.toString() ?? ''}
              onChange={e => setEpsilon(e.target.value ? parseFloat(e.target.value) : undefined)}
              placeholder={t('search.epsilon_placeholder')}
            />
          </FormField>
        </Form>
      </Section>

      <Section>
        {isPending && <Spinner size="lg" />}

        {isError && (
          <Panel variant="error">
            {t('search.error')}: {error?.message}
          </Panel>
        )}

        {data && (
          <div className="space-y-4">
            <Span variant="sectionTitle">{t('search.results')}</Span>
            {data.results.length === 0 ? (
              <Panel variant="raised">{t('search.no_results')}</Panel>
            ) : (
              data.results.map(result => (
                <Panel key={result.id} className="flex items-center justify-between">
                  <Span>{result.id}</Span>
                  <Span variant="muted">{result.distance.toFixed(4)}</Span>
                </Panel>
              ))
            )}
          </div>
        )}
      </Section>
    </GridLayout>
  );
}
