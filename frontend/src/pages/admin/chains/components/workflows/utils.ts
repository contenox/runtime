import dagre from 'dagre';
import { ChainTask, TransitionBranch } from '../../../../../lib/types';

export type LayoutDirection = 'horizontal' | 'vertical';
export type NodePosition = { id: string; x: number; y: number; width: number; height: number };
export type Edge = { from: string; to: string; label: string; isError?: boolean };

// Constants for layout
const NODE_WIDTH = 220;
const NODE_HEIGHT = 100;
const HORIZONTAL_SPACING = 80;
const VERTICAL_SPACING = 60;

export const calculateLayout = (
  tasks: ChainTask[],
  direction: LayoutDirection,
): { nodePositions: Record<string, NodePosition>; edges: Edge[] } => {
  if (tasks.length === 0) {
    return { nodePositions: {}, edges: [] };
  }

  // Create graph
  const graph = new dagre.graphlib.Graph();
  graph.setGraph({
    rankdir: direction === 'horizontal' ? 'LR' : 'TB',
    nodesep: HORIZONTAL_SPACING,
    ranksep: VERTICAL_SPACING,
    marginx: 50,
    marginy: 50,
  });
  graph.setDefaultEdgeLabel(() => ({}));

  // Add nodes
  tasks.forEach(task => {
    graph.setNode(task.id, {
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      label: task.id,
      taskType: task.handler,
    });
  });

  // Add edges
  const edges: Edge[] = [];

  tasks.forEach(task => {
    // Add failure transition
    if (task.transition.on_failure) {
      edges.push({
        from: task.id,
        to: task.transition.on_failure,
        label: 'on_failure',
        isError: true,
      });
      graph.setEdge(task.id, task.transition.on_failure);
    }

    // Add branch transitions
    task.transition.branches.forEach((branch: TransitionBranch) => {
      if (branch.goto && branch.goto !== 'end') {
        edges.push({
          from: task.id,
          to: branch.goto,
          label: `${branch.operator}: ${branch.when}`,
        });
        graph.setEdge(task.id, branch.goto);
      }
    });
  });

  // Calculate layout
  dagre.layout(graph);

  // Extract node positions
  const nodePositions: Record<string, NodePosition> = {};
  graph.nodes().forEach(id => {
    const node = graph.node(id);
    nodePositions[id] = {
      id,
      x: node.x - node.width / 2,
      y: node.y - node.height / 2,
      width: node.width,
      height: node.height,
    };
  });

  return { nodePositions, edges };
};

// Helper to get task color based on handler type
export const getTaskColor = (handler: string) => {
  switch (handler) {
    case 'condition_key':
      return 'bg-blue-100 border-blue-300';
    case 'hook':
      return 'bg-purple-100 border-purple-300';
    case 'model_execution':
    case 'embedding':
    case 'parse_transition':
      return 'bg-green-100 border-green-300';
    case 'parse_number':
    case 'parse_score':
    case 'parse_range':
      return 'bg-yellow-100 border-yellow-300';
    default:
      return 'bg-gray-100 border-gray-300';
  }
};

// Generate connector points for transitions
export const getConnectorPoints = (source: NodePosition, target: NodePosition) => {
  const startX = source.x + source.width / 2;
  const startY = source.y + source.height;
  const endX = target.x + target.width / 2;
  const endY = target.y;

  // Calculate control points for a smooth curve
  const distance = Math.sqrt(Math.pow(endX - startX, 2) + Math.pow(endY - startY, 2));
  const controlY = startY + Math.min(distance * 0.5, 200);

  return {
    start: { x: startX, y: startY },
    end: { x: endX, y: endY },
    control: { x: startX, y: controlY },
  };
};
