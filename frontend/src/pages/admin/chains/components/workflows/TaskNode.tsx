import { Edit } from 'lucide-react';
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
  const selectedClass = isSelected ? 'ring-2 ring-blue-500 shadow-md' : '';

  return (
    <g transform={`translate(${x}, ${y})`}>
      <rect
        width={width}
        height={height}
        rx="8"
        className={`${colorClass} border ${selectedClass} transition-all duration-200`}
      />

      <foreignObject width={width} height={height} className="p-3">
        <div className="flex h-full flex-col">
          <div className="flex items-start justify-between">
            <div>
              <div className="truncate text-sm font-bold">{task.id}</div>
              <div className="mb-1 text-xs text-gray-500">{task.handler}</div>
            </div>
            {onEdit && (
              <button
                onClick={e => {
                  e.stopPropagation();
                  onEdit();
                }}
                className="text-gray-500 transition-colors hover:text-blue-600"
                title="Edit task">
                <Edit className="h-4 w-4" />
              </button>
            )}
          </div>

          {task.description && (
            <p className="mb-1 line-clamp-2 flex-grow text-xs">{task.description}</p>
          )}

          <div className="mt-auto flex justify-between text-xs text-gray-500">
            <span>
              {task.transition.branches.length} branch
              {task.transition.branches.length !== 1 ? 'es' : ''}
            </span>
            {task.timeout && <span>{task.timeout}</span>}
          </div>
        </div>
      </foreignObject>
    </g>
  );
};

export default TaskNode;
