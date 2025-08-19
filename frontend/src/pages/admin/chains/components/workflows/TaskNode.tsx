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

      <foreignObject width={width} height={height} className="p-3 text-current">
        <div className="flex h-full flex-col font-sans">
          <div className="flex items-start justify-between">
            <div className="flex-grow overflow-hidden">
              <div className="truncate text-base font-bold" title={task.id}>
                {task.id}
              </div>
              {/* Using opacity for secondary text to maintain theme color */}
              <div className="mb-1 text-xs text-current opacity-80">{task.handler}</div>
            </div>
            {onEdit && (
              <button
                onClick={e => {
                  e.stopPropagation();
                  onEdit();
                }}
                // Icon uses opacity and transitions for a cleaner effect
                className="flex-shrink-0 p-1 text-current opacity-60 transition-opacity hover:opacity-100"
                title="Edit task in JSON">
                <Edit className="h-4 w-4" />
              </button>
            )}
          </div>

          {task.description && (
            <p
              className="mt-1 line-clamp-2 flex-grow text-xs text-current opacity-90"
              title={task.description}>
              {task.description}
            </p>
          )}

          <div className="mt-auto flex items-center justify-end text-xs text-current opacity-80">
            <GitBranch className="mr-1 h-3 w-3" />
            <span>
              {task.transition.branches.length} branch
              {task.transition.branches.length !== 1 && 'es'}
            </span>
          </div>
        </div>
      </foreignObject>
    </g>
  );
};

export default TaskNode;
