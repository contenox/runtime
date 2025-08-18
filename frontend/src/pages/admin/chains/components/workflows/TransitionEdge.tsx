import React from 'react';
import { getConnectorPoints, NodePosition } from './utils';

interface TransitionEdgeProps {
  source: NodePosition;
  target: NodePosition;
  label: string;
  isError?: boolean;
  isHighlighted?: boolean;
}

const TransitionEdge: React.FC<TransitionEdgeProps> = ({
  source,
  target,
  label,
  isError = false,
  isHighlighted = false,
}) => {
  if (!source || !target) return null;

  const { start, end, control } = getConnectorPoints(source, target);

  // Calculate quadratic bezier curve path
  const path = `M ${start.x},${start.y} Q ${start.x},${control.y} ${end.x},${end.y}`;

  // Calculate label position
  const labelX = (start.x + end.x) / 2;
  const labelY = (start.y + end.y) / 2 - 10;

  const strokeColor = isError ? '#ef4444' : isHighlighted ? '#3b82f6' : '#6b7280';
  const strokeWidth = isHighlighted ? 2 : 1;
  const dashArray = isError ? '4,2' : 'none';

  return (
    <g>
      <path
        d={path}
        fill="none"
        stroke={strokeColor}
        strokeWidth={strokeWidth}
        strokeDasharray={dashArray}
        className="transition-all duration-200"
      />

      {/* Arrowhead */}
      <polygon
        points={`${end.x - 4},${end.y - 8} ${end.x},${end.y} ${end.x + 4},${end.y - 8}`}
        fill={strokeColor}
        className="transition-all duration-200"
      />

      {/* Label */}
      <g transform={`translate(${labelX}, ${labelY})`}>
        <rect x="-50" y="-12" width="100" height="20" rx="4" fill="white" className="shadow-sm" />
        <text
          textAnchor="middle"
          dominantBaseline="middle"
          className="fill-gray-700 text-xs font-medium">
          {label}
        </text>
      </g>
    </g>
  );
};

export default TransitionEdge;
