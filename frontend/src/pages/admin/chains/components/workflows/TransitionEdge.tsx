import React from 'react';
import { getConnectorPath, LayoutDirection, NodePosition } from './utils';

interface TransitionEdgeProps {
  source: NodePosition;
  target: NodePosition;
  label: string;
  direction: LayoutDirection;
  isHighlighted?: boolean;
}

const getEdgeStrokeClass = (isHighlighted?: boolean, isError?: boolean): string => {
  if (isError) {
    return 'stroke-[var(--color-error)] dark:stroke-[var(--color-dark-error-500)]';
  }
  if (isHighlighted) {
    return 'stroke-[var(--color-dark-accent-400)] dark:stroke-[var(--color-dark-accent-400)]';
  }

  return 'stroke-[var(--color-dark-primary-500)] dark:stroke-[var(--color-dark-primary-500)]';
};

const TransitionEdge: React.FC<TransitionEdgeProps> = ({
  source,
  target,
  label,
  direction,
  isHighlighted = false,
}) => {
  if (!source || !target) return null;

  const path = getConnectorPath(source, target, direction);

  const labelX = (source.x + source.width / 2 + target.x + target.width / 2) / 2;
  const labelY = (source.y + source.height / 2 + target.y + target.height / 2) / 2;

  const strokeClass = getEdgeStrokeClass(isHighlighted);
  const strokeWidth = isHighlighted ? 2.5 : 1.5;

  return (
    <g>
      <path
        d={path}
        fill="none"
        className={`transition-all duration-300 ${strokeClass}`}
        strokeWidth={strokeWidth}
        strokeDasharray={'none'}
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
            className="fill-[var(--color-surface-100)] stroke-[var(--color-primary-100)] dark:fill-[var(--color-dark-surface-100)] dark:stroke-[var(--color-dark-surface-400)]"
          />
          <text
            textAnchor="middle"
            dominantBaseline="middle"
            className="fill-[var(--color-text)] text-xs font-medium dark:fill-[var(--color-dark-text)]">
            {label}
          </text>
        </g>
      )}
    </g>
  );
};

export default TransitionEdge;
