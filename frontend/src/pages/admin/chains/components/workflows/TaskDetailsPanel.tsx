import { Badge, Button, Card, H3, Label, P, Panel, Small } from '@contenox/ui';
import { Clock, GitBranch, RefreshCw, X } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { ChainTask } from '../../../../../lib/types';
import { getTaskColor } from './utils';

interface TaskDetailsPanelProps {
  task: ChainTask;
  onClose: () => void;
}

const TaskDetailsPanel: React.FC<TaskDetailsPanelProps> = ({ task, onClose }) => {
  const { t } = useTranslation();
  const colorClass = getTaskColor(task.handler);

  return (
    <Card
      variant="bordered"
      className="bg-surface-50 dark:bg-dark-surface-100 absolute top-0 right-0 h-full w-96 overflow-y-auto">
      <Panel
        variant="bordered"
        className="bg-surface-50 dark:bg-dark-surface-200 sticky top-0 z-10">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className={`h-3 w-3 rounded-full ${colorClass.replace('border', 'bg')}`}></div>
            <H3>{task.id}</H3>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label={t('common.close')}>
            <X className="h-4 w-4" />
          </Button>
        </div>
      </Panel>

      <div className="space-y-4 p-4">
        {/* Basic Information */}
        <Panel variant="surface">
          <Label>{t('workflow.task_type')}</Label>
          <Badge className="mt-2">{task.handler}</Badge>
        </Panel>

        {/* Description */}
        {task.description && (
          <Panel variant="surface">
            <Label>{t('workflow.description')}</Label>
            <P className="mt-2 text-sm">{task.description}</P>
          </Panel>
        )}

        {/* Timeout */}
        {task.timeout && (
          <Panel variant="surface">
            <div className="flex items-center gap-1">
              <Clock className="h-4 w-4" />
              <Label>{t('workflow.timeout')}</Label>
            </div>
            <P className="mt-2 text-sm">{task.timeout}</P>
          </Panel>
        )}

        {/* Retry Settings */}
        {task.retry_on_failure && (
          <Panel variant="surface">
            <div className="flex items-center gap-1">
              <RefreshCw className="h-4 w-4" />
              <Label>{t('workflow.retry_on_failure')}</Label>
            </div>
            <P className="mt-2 text-sm">
              {task.retry_on_failure} {t('workflow.times')}
            </P>
          </Panel>
        )}

        {/* Prompt Template */}
        {task.prompt_template && (
          <Panel variant="surface">
            <Label>{t('workflow.prompt_template')}</Label>
            <Panel
              variant="flat"
              className="bg-surface-100 dark:bg-dark-surface-200 mt-2 p-3 font-mono text-sm">
              {task.prompt_template}
            </Panel>
          </Panel>
        )}

        {/* Transition Settings */}
        <Panel variant="surface">
          <div className="flex items-center gap-1">
            <GitBranch className="h-4 w-4" />
            <Label>{t('workflow.transitions')}</Label>
          </div>

          <div className="mt-2 space-y-3">
            {/* On Failure Transition */}
            {task.transition.on_failure && (
              <Panel
                variant="flat"
                className="border-error-300 dark:border-l-dark-error-700 border-l-2 pl-3">
                <Small className="text-error-700 dark:text-dark-error-300 font-medium">
                  {t('workflow.on_failure')}: {task.transition.on_failure}
                </Small>
                {task.transition.alert_on_match && (
                  <Small className="text-error-500 dark:text-dark-error-400 mt-1 block">
                    {t('workflow.alert_on_match')}
                  </Small>
                )}
              </Panel>
            )}

            {/* Branches */}
            {task.transition.branches.length > 0 && (
              <div>
                <Label>
                  {t('workflow.branches')} ({task.transition.branches.length})
                </Label>
                <div className="mt-2 space-y-2">
                  {task.transition.branches.map((branch, index) => (
                    <Panel
                      key={index}
                      variant="flat"
                      className="border-primary-300 dark:border-l-dark-primary-700 border-l-2 pl-3">
                      <Small>
                        <span className="font-medium">When:</span> {branch.when}
                      </Small>
                      <Small className="block">
                        <span className="font-medium">Operator:</span>{' '}
                        {branch.operator || 'default'}
                      </Small>
                      <Small className="block">
                        <span className="font-medium">Go to:</span> {branch.goto}
                      </Small>
                      {branch.alert_on_match && (
                        <Small className="text-primary-500 dark:text-dark-primary-400 mt-1 block">
                          {t('workflow.alert_on_match')}
                        </Small>
                      )}
                    </Panel>
                  ))}
                </div>
              </div>
            )}
          </div>
        </Panel>

        {/* Hook Information */}
        {task.hook && (
          <Panel variant="surface">
            <Label>{t('workflow.hook')}</Label>
            <Panel variant="flat" className="bg-surface-100 dark:bg-dark-surface-200 mt-2 p-3">
              <Small className="font-mono">{task.hook.name}</Small>
              {Object.keys(task.hook.args).length > 0 && (
                <div className="mt-2">
                  <Label>Arguments:</Label>
                  <Panel
                    variant="flat"
                    className="bg-surface-200 dark:bg-dark-surface-300 mt-1 p-2">
                    <pre className="text-xs">{JSON.stringify(task.hook.args, null, 2)}</pre>
                  </Panel>
                </div>
              )}
            </Panel>
          </Panel>
        )}

        {/* Valid Conditions */}
        {task.valid_conditions && Object.keys(task.valid_conditions).length > 0 && (
          <Panel variant="surface">
            <Label>{t('workflow.valid_conditions')}</Label>
            <Panel variant="flat" className="bg-surface-100 dark:bg-dark-surface-200 mt-2 p-3">
              <pre className="text-xs">{JSON.stringify(task.valid_conditions, null, 2)}</pre>
            </Panel>
          </Panel>
        )}

        {/* Print Configuration */}
        {task.print && (
          <Panel variant="surface">
            <Label>{t('workflow.print_configuration')}</Label>
            <Panel variant="flat" className="bg-surface-100 dark:bg-dark-surface-200 mt-2 p-3">
              <Small>{task.print}</Small>
            </Panel>
          </Panel>
        )}
      </div>
    </Card>
  );
};

export default TaskDetailsPanel;
