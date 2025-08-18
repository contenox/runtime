import { GridLayout, Panel, Section, Spinner } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { useChain } from '../../../../hooks/useChains';
import { ChainTask } from '../../../../lib/types';
import ChainEditor from './ChainEditor';
import ChainVisualizer from './workflows/ChainVisualizer';

export default function ChainDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { t } = useTranslation();
  const { data: chain, isLoading, error } = useChain(id!);
  const [selectedTask, setSelectedTask] = useState<ChainTask | null>(null);
  const [taskToEdit, setTaskToEdit] = useState<string | null>(null);

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
    <GridLayout variant="body" className="h-full">
      <Section title={t('chains.editor_title', { id: chain.id })} className="h-full">
        <div className="grid h-full grid-cols-1 gap-6 lg:grid-cols-2">
          <ChainEditor
            chain={chain}
            selectedTask={selectedTask}
            onTaskSelect={setSelectedTask}
            highlightTaskId={taskToEdit}
            onHighlightReset={() => setTaskToEdit(null)}
          />
          <ChainVisualizer
            chain={chain}
            selectedTaskId={selectedTask?.id || null}
            onTaskSelect={task => {
              setSelectedTask(task);
              setTaskToEdit(null);
            }}
            onTaskEdit={taskId => {
              setTaskToEdit(taskId);
              // Scroll to task in editor
              setTimeout(() => {
                const element = document.getElementById(`task-${taskId}`);
                if (element) {
                  element.scrollIntoView({ behavior: 'smooth', block: 'center' });
                }
              }, 100);
            }}
          />
        </div>
      </Section>
    </GridLayout>
  );
}
