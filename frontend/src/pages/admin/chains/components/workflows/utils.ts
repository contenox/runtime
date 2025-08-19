import dagre from 'dagre';
import { ChainTask, TransitionBranch } from '../../../../../lib/types';

export type LayoutDirection = 'horizontal' | 'vertical';
export type NodePosition = { id: string; x: number; y: number; width: number; height: number };
export type Edge = { from: string; to: string; label: string; isError?: boolean; fromType: string };

const NODE_WIDTH = 250;
const NODE_HEIGHT = 200;
const HORIZONTAL_SPACING = 85;
const VERTICAL_SPACING = 100;

export const calculateLayout = (
  tasks: ChainTask[],
  direction: LayoutDirection,
): { nodePositions: Record<string, NodePosition>; edges: Edge[] } => {
  if (tasks.length === 0) {
    return { nodePositions: {}, edges: [] };
  }

  const graph = new dagre.graphlib.Graph();
  graph.setGraph({
    rankdir: direction === 'horizontal' ? 'LR' : 'TB',
    nodesep: HORIZONTAL_SPACING,
    ranksep: VERTICAL_SPACING,
    marginx: 25,
    marginy: 25,
  });
  graph.setDefaultEdgeLabel(() => ({}));

  tasks.forEach(task => {
    graph.setNode(task.id, {
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      label: task.id,
    });
  });

  const edges: Edge[] = [];
  tasks.forEach(task => {
    const fromType = task.handler;
    if (task.transition.on_failure) {
      edges.push({
        from: task.id,
        to: task.transition.on_failure,
        label: 'on_failure',
        isError: true,
        fromType,
      });
      graph.setEdge(task.id, task.transition.on_failure);
    }
    task.transition.branches.forEach((branch: TransitionBranch) => {
      if (branch.goto && branch.goto !== 'end') {
        edges.push({
          from: task.id,
          to: branch.goto,
          label: branch.when || 'next',
          fromType,
        });
        graph.setEdge(task.id, branch.goto);
      }
    });
  });

  dagre.layout(graph);

  const nodePositions: Record<string, NodePosition> = {};
  graph.nodes().forEach(id => {
    const node = graph.node(id);
    if (node) {
      nodePositions[id] = {
        id,
        x: node.x - node.width / 2,
        y: node.y - node.height / 2,
        width: node.width,
        height: node.height,
      };
    }
  });

  return { nodePositions, edges };
};

// Helper to classify task handlers
export const getTaskType = (handler: string): 'primary' | 'secondary' | 'accent' | 'default' => {
  if (
    [
      'moderate',
      'mux_input',
      'condition_key',
      'parse_number',
      'parse_score',
      'parse_range',
    ].includes(handler)
  ) {
    return 'primary';
  }
  if (
    ['execute_model_on_messages', 'search_knowledge', 'append_search_results'].includes(handler)
  ) {
    return 'secondary';
  }
  if (
    [
      'echo_message',
      'print_help_message',
      'do_we_need_context',
      'swap_to_input',
      'request_failed',
      'reject_request',
      'raise_error',
      'noop',
      'hook',
    ].includes(handler)
  ) {
    return 'accent';
  }
  return 'default';
};

export const getTaskColor = (handler: string): string => {
  const type = getTaskType(handler);
  switch (type) {
    case 'primary':
      return 'bg-surface-300 border-surface-300 text-text dark:bg-dark-surface-700 dark:border-dark-surface-600 dark:text-dark-text';
    case 'secondary':
      return 'bg-surface-300 border-surface-300 text-text dark:bg-dark-surface-700 dark:border-dark-surface-600 dark:text-dark-text';
    case 'accent':
      return 'bg-surface-300 border-surface-300 text-text dark:bg-dark-surface-700 dark:border-dark-surface-600 dark:text-dark-text';
    default:
      return 'bg-surface-300 border-surface-300 text-text dark:bg-dark-surface-700 dark:border-dark-surface-600 dark:text-dark-text';
  }
};

// Generate a smooth cubic Bezier curve path string
export const getConnectorPath = (
  source: NodePosition,
  target: NodePosition,
  direction: LayoutDirection,
): string => {
  if (direction === 'vertical') {
    const startX = source.x + source.width / 2;
    const startY = source.y + source.height;
    const endX = target.x + target.width / 2;
    const endY = target.y;
    const midY = startY + (endY - startY) / 2;
    return `M${startX},${startY} C${startX},${midY} ${endX},${midY} ${endX},${endY}`;
  } else {
    // horizontal
    const startX = source.x + source.width;
    const startY = source.y + source.height / 2;
    const endX = target.x;
    const endY = target.y + target.height / 2;
    const midX = startX + (endX - startX) / 2;
    return `M${startX},${startY} C${midX},${startY} ${midX},${endY} ${endX},${endY}`;
  }
};
