import {
  Button,
  Form,
  FormField,
  GridLayout,
  Panel,
  Section,
  Span,
  Spinner,
  Textarea,
} from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useExecPrompt } from '../../../hooks/useExec';

export default function ExecPromptPage() {
  const { t } = useTranslation();
  const [prompt, setPrompt] = useState('');
  const [executedPrompt, setExecutedPrompt] = useState<string | null>(null);

  const { mutate: executePrompt, data, isPending, isError, error } = useExecPrompt();

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (prompt.trim()) {
      executePrompt({ prompt });
      setExecutedPrompt(prompt);
    }
  };

  return (
    <GridLayout variant="body">
      <Section>
        <Form
          onSubmit={handleSubmit}
          title={t('prompt.title', 'Execute Prompt')}
          actions={
            <Button type="submit" variant="primary">
              {t('prompt.execute', 'Execute')}
            </Button>
          }>
          <FormField label={t('prompt.label', 'Prompt')} required>
            <Textarea
              value={prompt}
              onChange={e => setPrompt(e.target.value)}
              placeholder={t('prompt.placeholder', 'Enter your prompt')}
            />
          </FormField>
        </Form>
      </Section>

      <Section>
        {!executedPrompt && (
          <Panel>{t('prompt.invite', 'Enter a prompt to see the result.')}</Panel>
        )}

        {isPending && <Spinner size="lg" />}

        {isError && (
          <Panel variant="error">
            {t('prompt.error', 'Execution failed')}: {error?.message}
          </Panel>
        )}

        {data && (
          <Panel variant="raised" className="space-y-2">
            <Span variant="sectionTitle">{'>> '}</Span>
            <Span>{data.response}</Span>
          </Panel>
        )}
      </Section>
    </GridLayout>
  );
}
