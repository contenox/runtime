import { Button, Form, FormField, Input, Panel } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useConfigureProvider, useProviderStatus } from '../../../../hooks/useProviders';

type ProviderFormProps = {
  provider: 'openai' | 'gemini';
};

export default function ProviderForm({ provider }: ProviderFormProps) {
  const { t } = useTranslation();
  const [apiKey, setApiKey] = useState('');
  const { data: status, isLoading, error } = useProviderStatus(provider);
  const configureMutation = useConfigureProvider(provider);

  return (
    <Form
      title={t('cloud_providers.form_title')}
      onSubmit={() => {
        configureMutation.mutate({ apiKey, upsert: true });
      }}
      actions={
        <Button type="submit" variant="primary" disabled={configureMutation.isPending}>
          {configureMutation.isPending
            ? t('common.configuring')
            : t('cloud_providers.configure_button')}
        </Button>
      }>
      {isLoading && <Panel variant="body">{t('common.loading')}</Panel>}
      {error && <Panel variant="error">{error.message}</Panel>}

      <FormField label={t('cloud_providers.api_key')} required>
        <Input
          type="password"
          value={apiKey}
          onChange={e => setApiKey(e.target.value)}
          placeholder={t('cloud_providers.api_key_placeholder')}
        />
      </FormField>

      {status && (
        <Panel variant={status.configured ? 'flat' : 'flat'}>
          {' '}
          {status.configured
            ? t('cloud_providers.status_configured')
            : t('cloud_providers.status_not_configured')}
        </Panel>
      )}
    </Form>
  );
}
