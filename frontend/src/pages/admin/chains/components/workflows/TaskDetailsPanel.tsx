import { Button, Section, Table, TableCell, TableRow } from '@contenox/ui';
import {
  AlertCircle,
  ArrowRight,
  ChevronDown,
  ChevronUp,
  GitBranch,
  X,
  XCircle,
} from 'lucide-react';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChainTask, TaskTransition } from '../../../../../lib/types';

interface TaskDetailsPanelProps {
  task: ChainTask;
  onClose: () => void;
}

const TaskDetailsPanel: React.FC<TaskDetailsPanelProps> = ({ task, onClose }) => {
  const { t } = useTranslation();
  const [expandedSections, setExpandedSections] = useState<Record<string, boolean>>({
    transitions: true,
    conditions: true,
  });

  const toggleSection = (section: string) => {
    setExpandedSections(prev => ({
      ...prev,
      [section]: !prev[section],
    }));
  };

  const renderTransition = (transition: TaskTransition) => (
    <div className="space-y-3">
      {transition.on_failure && (
        <div className="flex items-start">
          <div className="mr-2 flex h-8 w-8 items-center justify-center rounded-full bg-red-100 text-red-800">
            <XCircle className="h-4 w-4" />
          </div>
          <div>
            <div className="font-medium">{t('workflow.on_failure')}</div>
            <div className="flex items-center text-sm text-gray-600">
              <ArrowRight className="mr-1 h-4 w-4" />
              {t('workflow.goto_task')}: {transition.on_failure}
            </div>
          </div>
        </div>
      )}

      {transition.branches.map((branch, index) => (
        <div key={index} className="flex items-start">
          <div className="mr-2 flex h-8 w-8 items-center justify-center rounded-full bg-green-100 text-green-800">
            <GitBranch className="h-4 w-4" />
          </div>
          <div>
            <div className="font-medium">
              {branch.operator}: {branch.when}
            </div>
            <div className="flex items-center text-sm text-gray-600">
              <ArrowRight className="mr-1 h-4 w-4" />
              {t('workflow.goto_task')}: {branch.goto}
            </div>
            {branch.alert_on_match && (
              <div className="mt-1 flex items-center text-sm text-yellow-600">
                <AlertCircle className="mr-1 h-4 w-4" />
                {branch.alert_on_match}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );

  return (
    <div className="absolute top-0 right-0 bottom-0 z-10 flex w-96 flex-col border-l bg-white shadow-lg">
      <div className="flex items-center justify-between border-b p-4">
        <h3 className="text-lg font-semibold">{task.id}</h3>
        <Button size="sm" variant="ghost" onClick={onClose} title={t('common.close')}>
          <X className="h-5 w-5" />
        </Button>
      </div>

      <div className="flex-grow overflow-auto p-4">
        <Section>
          <div className="space-y-4">
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700">
                {t('workflow.handler_type')}
              </label>
              <div className="rounded-md bg-gray-100 px-3 py-2">{task.handler}</div>
            </div>

            {task.description && (
              <div>
                <label className="mb-1 block text-sm font-medium text-gray-700">
                  {t('workflow.description')}
                </label>
                <div className="rounded-md bg-gray-100 px-3 py-2">{task.description}</div>
              </div>
            )}
          </div>
        </Section>

        <Section>
          <button
            className="flex w-full items-center justify-between py-2 text-left"
            onClick={() => toggleSection('transitions')}>
            <h3 className="font-medium">{t('workflow.transitions')}</h3>
            {expandedSections.transitions ? (
              <ChevronUp className="h-4 w-4" />
            ) : (
              <ChevronDown className="h-4 w-4" />
            )}
          </button>

          {expandedSections.transitions && (
            <div className="mt-2">{renderTransition(task.transition)}</div>
          )}
        </Section>

        {task.prompt_template && (
          <Section>
            <button
              className="flex w-full items-center justify-between py-2 text-left"
              onClick={() => toggleSection('prompt')}>
              <h3 className="font-medium">{t('workflow.prompt_template')}</h3>
              {expandedSections.prompt ? (
                <ChevronUp className="h-4 w-4" />
              ) : (
                <ChevronDown className="h-4 w-4" />
              )}
            </button>

            {expandedSections.prompt && (
              <div className="mt-2 rounded-md bg-gray-100 px-3 py-2 font-mono text-sm whitespace-pre-wrap">
                {task.prompt_template}
              </div>
            )}
          </Section>
        )}

        {task.hook && (
          <Section>
            <button
              className="flex w-full items-center justify-between py-2 text-left"
              onClick={() => toggleSection('hook')}>
              <h3 className="font-medium">{t('workflow.hook_config')}</h3>
              {expandedSections.hook ? (
                <ChevronUp className="h-4 w-4" />
              ) : (
                <ChevronDown className="h-4 w-4" />
              )}
            </button>

            {expandedSections.hook && (
              <div className="mt-2">
                <div className="rounded-md bg-gray-100 px-3 py-2">
                  <div className="font-medium">{task.hook.name}</div>
                  <div className="mt-2 text-sm">
                    {Object.entries(task.hook.args || {}).map(([key, value]) => (
                      <div key={key} className="flex items-center border-b border-gray-200 py-1">
                        <span className="w-32 truncate font-medium">{key}:</span>
                        <span className="truncate">{value}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )}
          </Section>
        )}

        {task.valid_conditions && (
          <Section>
            <button
              className="flex w-full items-center justify-between py-2 text-left"
              onClick={() => toggleSection('conditions')}>
              <h3 className="font-medium">{t('workflow.valid_conditions')}</h3>
              {expandedSections.conditions ? (
                <ChevronUp className="h-4 w-4" />
              ) : (
                <ChevronDown className="h-4 w-4" />
              )}
            </button>

            {expandedSections.conditions && (
              <div className="mt-2">
                <Table columns={['Condition', 'Status']}>
                  {Object.entries(task.valid_conditions).map(([condition, enabled], idx) => (
                    <TableRow key={idx}>
                      <TableCell>{condition}</TableCell>
                      <TableCell>
                        {enabled ? (
                          <span className="flex items-center text-green-600">
                            <div className="mr-2 h-2 w-2 rounded-full bg-green-500"></div>
                            Active
                          </span>
                        ) : (
                          <span className="flex items-center text-gray-500">
                            <div className="mr-2 h-2 w-2 rounded-full bg-gray-400"></div>
                            Inactive
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </Table>
              </div>
            )}
          </Section>
        )}
      </div>
    </div>
  );
};

export default TaskDetailsPanel;
