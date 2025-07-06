import { Badge, Card } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { ChainTask } from '../../../../lib/types';

interface TaskNodeProps {
  task: ChainTask;
  index: number;
  isLast: boolean;
}

export default function TaskNode({ task, isLast }: TaskNodeProps) {
  const { t } = useTranslation();

  const taskTypeMap: Record<string, string> = {
    llm: t('chains.task_type_llm'),
    hook: t('chains.task_type_hook'),
    condition_key: t('chains.task_type_condition'),
    // Add other types as needed
  };

  return (
    <Card variant="bordered" className="relative w-64 p-3">
      <div className="flex items-start justify-between">
        <div>
          <div className="font-bold">{task.id}</div>
          <div className="text-surface-500 text-sm">{task.description}</div>
        </div>
        <Badge variant="default">{taskTypeMap[task.type] || task.type}</Badge>
      </div>

      <div className="mt-2 text-sm">
        {task.type === 'hook' && task.hook && (
          <div>
            <div className="font-medium">{t('chains.hook_type')}</div>
            <div className="font-mono">{task.hook.type}</div>
          </div>
        )}

        {task.prompt_template && (
          <div className="mt-1">
            <div className="font-medium">{t('chains.prompt_template')}</div>
            <div className="truncate">{task.prompt_template}</div>
          </div>
        )}
      </div>

      {!isLast && (
        <div className="absolute -bottom-4 left-1/2 -translate-x-1/2 transform">
          <div className="bg-surface-300 mx-auto h-4 w-0.5"></div>
          <div className="mt-1 text-center text-xs">↓</div>
        </div>
      )}
    </Card>
  );
}
