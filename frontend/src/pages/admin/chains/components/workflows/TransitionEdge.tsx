import React from 'react';
import { getConnectorPath, getTaskType, LayoutDirection, NodePosition } from './utils';

interface TransitionEdgeProps {
  source: NodePosition;
  target: NodePosition;
  label: string;
  direction: LayoutDirection;
  fromType: string;
  isError?: boolean;
  isHighlighted?: boolean;
}

const getEdgeStrokeClass = (
  handler: string,
  isHighlighted?: boolean,
  isError?: boolean,
): string => {
  if (isError) {
    return 'stroke-[var(--color-error)] dark:stroke-[var(--color-dark-error-500)]';
  }
  if (isHighlighted) {
    return 'stroke-[var(--color-accent-600)] dark:stroke-[var(--color-dark-accent-400)]';
  }

  const type = getTaskType(handler);
  switch (type) {
    case 'primary':
      return 'stroke-[var(--color-primary)/0.8] dark:stroke-[var(--color-dark-primary-500)/0.8]';
    case 'secondary':
      return 'stroke-[var(--color-secondary)/0.8] dark:stroke-[var(--color-dark-secondary-500)/0.8]';
    case 'accent':
      return 'stroke-[var(--color-accent)/0.8] dark:stroke-[var(--color-dark-accent-500)/0.8]';
    default:
      return 'stroke-[var(--color-surface-400)] dark:stroke-[var(--color-dark-surface-500)]';
  }
};

const TransitionEdge: React.FC<TransitionEdgeProps> = ({
  source,
  target,
  label,
  direction,
  fromType,
  isError = false,
  isHighlighted = false,
}) => {
  if (!source || !target) return null;

  const path = getConnectorPath(source, target, direction);

  const labelX = (source.x + source.width / 2 + target.x + target.width / 2) / 2;
  const labelY = (source.y + source.height / 2 + target.y + target.height / 2) / 2;

  const strokeClass = getEdgeStrokeClass(fromType, isHighlighted, isError);
  const strokeWidth = isHighlighted ? 2.5 : 1.5;
  const dashArray = isError ? '5 3' : 'none';

  return (
    <g>
      <path
        d={path}
        fill="none"
        className={`transition-all duration-300 ${strokeClass}`}
        strokeWidth={strokeWidth}
        strokeDasharray={dashArray}
        markerEnd="url(#arrowhead)"
      />

      {label && label !== 'next' && (
        <g transform={`translate(${labelX}, ${labelY})`}>
          <rect
            x="-45"
            y="-11"
            width="90"
            height="22"
            rx="6"
            strokeWidth="1"
            // The fill is now a darker surface color in dark mode.
            // The stroke is now aware of dark mode for better visibility.
            className="fill-[var(--color-surface-100)] stroke-[rgba(0,0,0,0.05)] dark:fill-[var(--color-dark-surface-100)] dark:stroke-[rgba(255,255,255,0.05)]"
          />
          <text
            textAnchor="middle"
            dominantBaseline="middle"
            className="fill-[var(--color-text-muted)] text-xs font-medium dark:fill-[var(--color-dark-text-muted)]">
            {label}
          </text>
        </g>
      )}
    </g>
  );
};

export default TransitionEdge;
