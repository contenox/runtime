import { Panel } from '@contenox/ui';
import { Edit, GitBranch } from 'lucide-react';
import React from 'react';
import { ChainTask } from '../../../../../lib/types';
import { getTaskColor } from './utils';

interface TaskNodeProps {
  task: ChainTask;
  position: { x: number; y: number; width: number; height: number };
  isSelected: boolean;
  onEdit?: () => void;
}

const TaskNode: React.FC<TaskNodeProps> = ({ task, position, isSelected, onEdit }) => {
  const { x, y, width, height } = position;
  const colorClass = getTaskColor(task.handler);

  // Updated selection style with proper dark mode variants
  const selectedClass = isSelected
    ? 'ring-2 ring-offset-4 ring-offset-[var(--color-surface-100)] dark:ring-offset-[var(--color-dark-surface-50)] ring-accent-500 dark:ring-dark-accent-400 shadow-xl'
    : 'shadow-md';

  return (
    <g transform={`translate(${x}, ${y})`}>
      <rect
        width={width}
        height={height}
        rx="12"
        className={`${colorClass} ${selectedClass} transition-all duration-300 ease-in-out`}
      />

      <foreignObject
        width={width}
        height={height}
        className="bg-primary-100 border-surface-300 dark:bg-dark-primary-50 dark:border-dark-surface-600">
        <Panel variant="body" className="flex h-full flex-col">
          <div className="flex items-start justify-between">
            <div className="flex-grow overflow-hidden">
              <div className="truncate" title={task.id}>
                {task.id}
              </div>
              <div className="mb-1">{task.handler}</div>
            </div>
            {onEdit && (
              <button
                onClick={e => {
                  e.stopPropagation();
                  onEdit();
                }}
                className="flex-shrink-0 p-1"
                title="Edit task in JSON">
                <Edit className="h-4 w-4" />
              </button>
            )}
          </div>

          {task.description && (
            <Panel className="mt-1 line-clamp-2 flex-grow" title={task.description} variant="body">
              {task.description}
            </Panel>
          )}

          <div className="mt-auto flex items-center justify-end">
            <GitBranch className="mr-1 h-3 w-3" />
            <span>
              {task.transition.branches.length} branch
              {task.transition.branches.length !== 1 && 'es'}
            </span>
          </div>
        </Panel>
      </foreignObject>
    </g>
  );
};

export default TaskNode;
