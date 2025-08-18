import { Button, Card } from '@contenox/ui';
import { ArrowRight, ChevronRight, Maximize2, Workflow, ZoomIn, ZoomOut } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
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
  onTaskEdit,
}) => {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const [layoutDirection, setLayoutDirection] = useState<LayoutDirection>('horizontal');
  const [zoomLevel, setZoomLevel] = useState(1);
  const [panning, setPanning] = useState(false);
  const [startPoint, setStartPoint] = useState({ x: 0, y: 0 });
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [showDetails, setShowDetails] = useState(false);
  const [dimensions, setDimensions] = useState({ width: 0, height: 0 });

  // Calculate layout
  const { nodePositions, edges } = calculateLayout(chain.tasks, layoutDirection);

  // Handle window resize
  useEffect(() => {
    const updateDimensions = () => {
      if (containerRef.current) {
        setDimensions({
          width: containerRef.current.clientWidth,
          height: containerRef.current.clientHeight,
        });
      }
    };

    window.addEventListener('resize', updateDimensions);
    updateDimensions();

    return () => window.removeEventListener('resize', updateDimensions);
  }, []);

  // Panning handlers
  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button !== 1) return; // Only middle mouse button
    setPanning(true);
    setStartPoint({ x: e.clientX - offset.x, y: e.clientY - offset.y });
  };

  const handleMouseMove = (e: React.MouseEvent) => {
    if (!panning) return;
    setOffset({
      x: e.clientX - startPoint.x,
      y: e.clientY - startPoint.y,
    });
  };

  const handleMouseUp = () => {
    setPanning(false);
  };

  // Zoom handlers
  const handleZoomIn = () => setZoomLevel(prev => Math.min(prev + 0.1, 2));
  const handleZoomOut = () => setZoomLevel(prev => Math.max(prev - 0.1, 0.5));
  const handleZoomReset = () => {
    setZoomLevel(1);
    setOffset({ x: 0, y: 0 });
  };

  // Calculate viewBox based on layout
  const viewBox = (() => {
    if (!nodePositions || Object.keys(nodePositions).length === 0) {
      return '0 0 100 100';
    }

    const positions = Object.values(nodePositions);
    const minX = Math.min(...positions.map(p => p.x));
    const maxX = Math.max(...positions.map(p => p.x + p.width));
    const minY = Math.min(...positions.map(p => p.y));
    const maxY = Math.max(...positions.map(p => p.y + p.height));

    const padding = 50;
    return `${minX - padding} ${minY - padding} ${maxX - minX + padding * 2} ${maxY - minY + padding * 2}`;
  })();

  // Auto-select first task if none selected
  useEffect(() => {
    if (chain.tasks.length > 0 && !selectedTaskId && !showDetails) {
      onTaskSelect(chain.tasks[0]);
    }
  }, [chain.tasks, selectedTaskId, onTaskSelect, showDetails]);

  // Find the selected task
  const selectedTask = selectedTaskId ? chain.tasks.find(task => task.id === selectedTaskId) : null;

  return (
    <Card
      ref={containerRef}
      className="relative flex h-full flex-col overflow-hidden"
      onMouseDown={handleMouseDown}
      onMouseMove={handleMouseMove}
      onMouseUp={handleMouseUp}
      onMouseLeave={handleMouseUp}>
      <div className="flex items-center justify-between border-b p-3">
        <h3 className="flex items-center gap-2 text-lg font-semibold">
          <Workflow className="h-5 w-5" />
          {t('workflow.visualization_title')}
        </h3>
        <div className="flex items-center gap-2">
          <LayoutControls direction={layoutDirection} onChangeDirection={setLayoutDirection} />

          <div className="flex items-center gap-1">
            <Button
              size="sm"
              variant="ghost"
              onClick={handleZoomOut}
              title={t('workflow.zoom_out')}>
              <ZoomOut className="h-4 w-4" />
            </Button>

            <span className="w-12 text-center text-sm">{Math.round(zoomLevel * 100)}%</span>

            <Button size="sm" variant="ghost" onClick={handleZoomIn} title={t('workflow.zoom_in')}>
              <ZoomIn className="h-4 w-4" />
            </Button>

            <Button
              size="sm"
              variant="ghost"
              onClick={handleZoomReset}
              title={t('workflow.reset_view')}>
              <Maximize2 className="h-4 w-4" />
            </Button>
          </div>

          <Button
            size="sm"
            variant={showDetails ? 'primary' : 'secondary'}
            onClick={() => setShowDetails(!showDetails)}
            className="flex items-center gap-1">
            {showDetails ? (
              <>
                <ChevronRight className="h-4 w-4" />
                {t('workflow.hide_details')}
              </>
            ) : (
              <>
                <ChevronRight className="h-4 w-4 rotate-180" />
                {t('workflow.show_details')}
              </>
            )}
          </Button>
        </div>
      </div>

      <div className="relative flex-grow overflow-hidden bg-gray-50">
        {dimensions.width > 0 && (
          <svg
            width="100%"
            height="100%"
            viewBox={viewBox}
            className="cursor-move"
            preserveAspectRatio="xMidYMid meet"
            style={{
              transform: `scale(${zoomLevel}) translate(${offset.x}px, ${offset.y}px)`,
              transformOrigin: '0 0',
            }}>
            {/* Render edges first so they appear behind nodes */}
            {edges.map((edge, index) => (
              <TransitionEdge
                key={`edge-${index}`}
                source={nodePositions[edge.from]}
                target={nodePositions[edge.to]}
                label={edge.label}
                isError={edge.isError}
                isHighlighted={selectedTaskId === edge.from || selectedTaskId === edge.to}
              />
            ))}

            {/* Render task nodes */}
            {chain.tasks.map(task => (
              <g key={task.id} onClick={() => onTaskSelect(task)} className="cursor-pointer">
                <TaskNode
                  task={task}
                  position={nodePositions[task.id]}
                  isSelected={selectedTaskId === task.id}
                  onEdit={onTaskEdit ? () => onTaskEdit(task.id) : undefined}
                />
              </g>
            ))}
          </svg>
        )}

        {/* Watermark when no tasks */}
        {chain.tasks.length === 0 && (
          <div className="absolute inset-0 flex items-center justify-center">
            <div className="bg-opacity-80 rounded-lg bg-white p-8 text-center">
              <Workflow className="mx-auto mb-4 h-12 w-12 text-gray-300" />
              <p className="mt-4 text-gray-500">{t('workflow.no_tasks')}</p>
            </div>
          </div>
        )}

        {/* Panning indicator */}
        {panning && (
          <div className="absolute top-2 right-2 flex items-center gap-1 rounded bg-blue-500 px-2 py-1 text-xs text-white">
            <ArrowRight className="h-3 w-3" />
            {t('workflow.panning_mode')}
          </div>
        )}
      </div>

      {/* Task details panel */}
      {showDetails && selectedTask && (
        <TaskDetailsPanel task={selectedTask} onClose={() => setShowDetails(false)} />
      )}

      {/* Legend */}
      <div className="flex flex-wrap gap-4 border-t bg-white p-2 text-xs">
        <div className="flex items-center gap-2">
          <div className="h-3 w-3 rounded-sm border border-blue-300 bg-blue-100"></div>
          <span>{t('workflow.node_condition')}</span>
        </div>
        <div className="flex items-center gap-2">
          <div className="h-3 w-3 rounded-sm border border-green-300 bg-green-100"></div>
          <span>{t('workflow.node_model')}</span>
        </div>
        <div className="flex items-center gap-2">
          <div className="h-3 w-3 rounded-sm border border-purple-300 bg-purple-100"></div>
          <span>{t('workflow.node_hook')}</span>
        </div>
        <div className="flex items-center gap-2">
          <div className="h-3 w-3 rounded-sm border border-yellow-300 bg-yellow-100"></div>
          <span>{t('workflow.node_parser')}</span>
        </div>
        <div className="flex items-center gap-2">
          <div className="h-3 w-3">
            <svg viewBox="0 0 20 20">
              <path d="M0,20 L20,0" stroke="red" strokeWidth="2" />
            </svg>
          </div>
          <span>{t('workflow.edge_error')}</span>
        </div>
      </div>
    </Card>
  );
};

export default ChainVisualizer;
