import { Card, H3, Panel, Section } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { ChainDefinition, ChainTask } from '../../../../lib/types';
import TaskNode from './TaskNode';

interface ChainPreviewProps {
  chain: ChainDefinition;
}

export default function ChainPreview({ chain }: ChainPreviewProps) {
  const { t } = useTranslation();

  return (
    <Card variant="surface" className="p-4">
      <H3 className="mb-4">{t('chains.preview_title')}</H3>

      <div className="relative min-h-[300px]">
        <div className="flex flex-wrap gap-4">
          {chain.tasks.map((task: ChainTask, index: number) => (
            <TaskNode
              key={`${task.id}-${index}`}
              task={task}
              index={index}
              isLast={index === chain.tasks.length - 1}
            />
          ))}
        </div>

        {/* Connection lines would be implemented here with SVG */}
      </div>

      <Section className="mt-4">
        <Panel variant="flat">{t('chains.preview_hint')}</Panel>
      </Section>
    </Card>
  );
}
