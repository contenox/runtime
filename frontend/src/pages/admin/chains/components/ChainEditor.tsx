import { Button, Card, FormField, Label, Spinner, Textarea } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useUpdateChain } from '../../../../hooks/useChains';
import { ChainDefinition } from '../../../../lib/types';

interface ChainEditorProps {
  chain: ChainDefinition;
}

export default function ChainEditor({ chain }: ChainEditorProps) {
  const { t } = useTranslation();
  const updateChain = useUpdateChain(chain.id);
  const [tasks, setTasks] = useState(JSON.stringify(chain.tasks, null, 2));
  const [tasksError, setTasksError] = useState('');

  const handleSave = () => {
    try {
      const parsedTasks = JSON.parse(tasks);
      updateChain.mutate({
        ...chain,
        tasks: parsedTasks,
      });
      setTasksError('');
    } catch (err) {
      setTasksError(t('chains.invalid_json') + err);
    }
  };

  return (
    <Card variant="surface" className="p-4">
      <div className="mb-4">
        <Label className="mb-2 block">{t('chains.form_id')}</Label>
        <div className="bg-surface-100 rounded p-2 font-mono">{chain.id}</div>
      </div>

      <div className="mb-4">
        <Label className="mb-2 block">{t('chains.form_description')}</Label>
        <div className="bg-surface-100 rounded p-2">{chain.description}</div>
      </div>

      <FormField label={t('chains.form_tasks')} error={tasksError}>
        <Textarea
          value={tasks}
          onChange={e => setTasks(e.target.value)}
          className="min-h-[400px] font-mono text-sm"
        />
      </FormField>

      <div className="mt-4 flex justify-end">
        <Button variant="primary" onClick={handleSave} disabled={updateChain.isPending}>
          {updateChain.isPending ? <Spinner size="sm" /> : t('common.save')}
        </Button>
      </div>
    </Card>
  );
}
