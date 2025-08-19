import { Button, Section } from '@contenox/ui';
import { ChevronRight, Maximize2, Workflow, ZoomIn, ZoomOut } from 'lucide-react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChainDefinition, ChainTask } from '../../../../../lib/types';
import LayoutControls from './LayoutControls';
import TaskDetailsPanel from './TaskDetailsPanel';
import TaskNode from './TaskNode';
import TransitionEdge from './TransitionEdge';
import { calculateLayout, LayoutDirection } from './utils';

interface ChainVisualizerProps {
  chain: ChainDefinition;
  selectedTaskId?: string | null;
  onTaskSelect: (task: ChainTask) => void;
  onTaskEdit?: (taskId: string) => void;
}

const ChainVisualizer: React.FC<ChainVisualizerProps> = ({
  chain,
  selectedTaskId,
  onTaskSelect,
}) => {
  const { t } = useTranslation();
  const svgRef = useRef<SVGSVGElement>(null);
  const [layoutDirection, setLayoutDirection] = useState<LayoutDirection>('horizontal');
  const [viewBox, setViewBox] = useState({ x: 0, y: 0, width: 1000, height: 1000 });
  const [zoom, setZoom] = useState(1);
  const [isPanning, setIsPanning] = useState(false);
  const [startPoint, setStartPoint] = useState({ x: 0, y: 0 });
  const [showDetails, setShowDetails] = useState(false);

  const { nodePositions, edges } = React.useMemo(() => {
    return calculateLayout(chain.tasks, layoutDirection);
  }, [chain.tasks, layoutDirection]);

  const calculateViewBox = useCallback(() => {
    if (Object.keys(nodePositions).length === 0) {
      return { x: -500, y: -500, width: 1000, height: 1000 };
    }

    const positions = Object.values(nodePositions);
    const minX = Math.min(...positions.map(p => p.x));
    const maxX = Math.max(...positions.map(p => p.x + p.width));
    const minY = Math.min(...positions.map(p => p.y));
    const maxY = Math.max(...positions.map(p => p.y + p.height));

    const padding = 100;
    const contentWidth = maxX - minX + padding * 2;
    const contentHeight = maxY - minY + padding * 2;

    return {
      x: minX - padding,
      y: minY - padding,
      width: contentWidth,
      height: contentHeight,
    };
  }, [nodePositions]);

  useEffect(() => {
    const newViewBox = calculateViewBox();

    if (
      viewBox.x !== newViewBox.x ||
      viewBox.y !== newViewBox.y ||
      viewBox.width !== newViewBox.width ||
      viewBox.height !== newViewBox.height
    ) {
      setViewBox(newViewBox);
      setZoom(1);
    }
  }, [calculateViewBox, viewBox]);

  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button !== 0 || e.target !== svgRef.current) return;
    setIsPanning(true);
    setStartPoint({ x: e.clientX, y: e.clientY });
  };

  const handleMouseMove = (e: React.MouseEvent) => {
    if (!isPanning) return;
    const clientWidth = svgRef.current?.clientWidth ?? viewBox.width;
    const zoomFactor = clientWidth / (viewBox.width / zoom);
    const dx = (e.clientX - startPoint.x) / zoomFactor;
    const dy = (e.clientY - startPoint.y) / zoomFactor;

    setViewBox(prev => ({ ...prev, x: prev.x - dx, y: prev.y - dy }));
    setStartPoint({ x: e.clientX, y: e.clientY });
  };

  const handleMouseUp = () => setIsPanning(false);

  const handleZoom = (factor: number) => setZoom(prev => Math.max(0.2, Math.min(prev * factor, 3)));

  const selectedTask = selectedTaskId ? chain.tasks.find(task => task.id === selectedTaskId) : null;

  return (
    <Section
      className="relative m-0 flex h-full flex-col overflow-hidden border-0 p-0"
      variant="bordered">
      <div className="z-10 flex items-center justify-between border-b p-2">
        <h3 className="flex items-center gap-2 text-lg font-semibold">
          <Workflow className="h-5 w-5" />
          {t('workflow.visualization_title')}
        </h3>
        <div className="flex items-center gap-2">
          <LayoutControls direction={layoutDirection} onChangeDirection={setLayoutDirection} />
          <div className="h-6 w-px"></div>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => handleZoom(0.8)}
            aria-label={t('workflow.zoom_out')}>
            <ZoomOut className="h-4 w-4" />
          </Button>
          <span className="w-12 text-center text-sm font-medium tabular-nums">
            {Math.round(zoom * 100)}%
          </span>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => handleZoom(1.25)}
            aria-label={t('workflow.zoom_in')}>
            <ZoomIn className="h-4 w-4" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => {
              const newViewBox = calculateViewBox();
              setViewBox(newViewBox);
              setZoom(1);
            }}
            aria-label={t('workflow.reset_view')}>
            <Maximize2 className="h-4 w-4" />
          </Button>
          <div className="h-6 w-px"></div>
          <Button size="sm" variant="secondary" onClick={() => setShowDetails(!showDetails)}>
            {t('workflow.show_details')}
            <ChevronRight
              className={`h-4 w-4 transition-transform duration-300 ${showDetails ? 'rotate-180' : ''}`}
            />
          </Button>
        </div>
      </div>

      <div className="bg-grid-pattern relative flex-grow overflow-hidden" onMouseUp={handleMouseUp}>
        <svg
          ref={svgRef}
          width="100%"
          height="100%"
          viewBox={`${viewBox.x} ${viewBox.y} ${viewBox.width / zoom} ${viewBox.height / zoom}`}
          onMouseDown={handleMouseDown}
          onMouseMove={handleMouseMove}
          onMouseLeave={handleMouseUp}
          className={isPanning ? 'cursor-grabbing' : 'cursor-grab'}>
          <defs>
            <marker
              id="arrowhead"
              viewBox="0 0 10 10"
              refX="8"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
            </marker>
          </defs>

          <g>
            {edges.map((edge, index) => (
              <TransitionEdge
                key={`edge-${index}`}
                source={nodePositions[edge.from]}
                target={nodePositions[edge.to]}
                label={edge.label}
                direction={layoutDirection}
                isHighlighted={selectedTaskId === edge.from || selectedTaskId === edge.to}
              />
            ))}
            {chain.tasks.map(
              task =>
                nodePositions[task.id] && (
                  <g key={task.id} onClick={() => onTaskSelect(task)} className="cursor-pointer">
                    <TaskNode
                      task={task}
                      position={nodePositions[task.id]}
                      isSelected={selectedTaskId === task.id}
                    />
                  </g>
                ),
            )}
          </g>
        </svg>

        {chain.tasks.length === 0 && (
          <div className="absolute inset-0 flex items-center justify-center text-center">
            <div className="rounded-lg p-8 backdrop-blur-sm">
              <Workflow className="text-surface-300 dark:text-dark-surface-500 mx-auto mb-4 h-12 w-12" />
              <p className="text-text-muted dark:text-dark-text-muted mt-2">
                {t('workflow.no_tasks')}
              </p>
            </div>
          </div>
        )}
      </div>

      {showDetails && selectedTask && (
        <TaskDetailsPanel task={selectedTask} onClose={() => setShowDetails(false)} />
      )}
    </Section>
  );
};

export default ChainVisualizer;
