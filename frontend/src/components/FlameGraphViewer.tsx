import { useMemo, useState } from 'react';
import type { FlameNode } from '../api';
import './FlameGraphViewer.css';

const ROW_HEIGHT = 22;
const MIN_WIDTH_PX = 4;

export interface FlameGraphViewerProps {
  data: FlameNode;
  width: number;
  height?: number;
  className?: string;
}

function computeMaxDepth(node: FlameNode): number {
  if (!node.children?.length) return 1;
  return 1 + Math.max(...node.children.map(computeMaxDepth));
}

interface LayoutNode {
  id: string;
  name: string;
  value: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

function computeLayout(root: FlameNode, totalWidth: number, totalHeight: number): LayoutNode[] {
  const out: LayoutNode[] = [];
  const rootValue = root.value || 1;
  let idCounter = 0;
  const id = () => `n${++idCounter}`;

  function layout(n: FlameNode, depth: number, left: number, width: number): void {
    const nodeId = id();
    const w = Math.max((n.value / rootValue) * width, n.children?.length ? 0 : MIN_WIDTH_PX);
    const y = depth * ROW_HEIGHT;
    if (w >= MIN_WIDTH_PX && y < totalHeight) {
      out.push({
        id: nodeId,
        name: n.name,
        value: n.value,
        x: left,
        y,
        w,
        h: ROW_HEIGHT - 1,
      });
    }
    if (!n.children?.length) return;
    let x = left;
    for (const c of n.children) {
      const cw = (c.value / rootValue) * width;
      if (cw >= MIN_WIDTH_PX) {
        layout(c, depth + 1, x, cw);
      }
      x += (c.value / rootValue) * width;
    }
  }

  layout(root, 0, 0, totalWidth);
  return out;
}

function formatValue(value: number, unit?: string, valueType?: string): string {
  if (unit === 'bytes') {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
    return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  }
  if (unit === 'nanoseconds') {
    const ms = value / 1e6;
    return ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(2)} s`;
  }
  // count or unknown: show the raw number with the sample type as label (e.g. "alloc_objects")
  const label = valueType || unit;
  return label ? `${value.toLocaleString()} ${label}` : value.toLocaleString();
}

function getColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h << 5) - h + name.charCodeAt(i);
  h = Math.abs(h) % 360;
  return `hsl(${h}, 65%, 50%)`;
}

export function FlameGraphViewer({ data, width, height, className = '' }: FlameGraphViewerProps) {
  const [hovered, setHovered] = useState<LayoutNode | null>(null);
  const [tooltipPos, setTooltipPos] = useState({ x: 0, y: 0 });

  const computedHeight = useMemo(
    () => height ?? Math.max(computeMaxDepth(data) * ROW_HEIGHT + 8, 60),
    [data, height]
  );

  const nodes = useMemo(
    () => computeLayout(data, width, computedHeight),
    [data, width, computedHeight]
  );

  const handleMouseMove = (e: React.MouseEvent<SVGElement>, node: LayoutNode) => {
    setHovered(node);
    setTooltipPos({ x: e.clientX, y: e.clientY });
  };

  return (
    <div className={`flame-graph-viewer ${className}`}>
      <svg
        width={width}
        height={computedHeight}
        viewBox={`0 0 ${width} ${computedHeight}`}
        onMouseLeave={() => setHovered(null)}
      >
        {nodes.map((node) => (
          <g key={node.id}>
            <rect
              x={node.x + 1}
              y={node.y + 2}
              width={Math.max(node.w - 2, 1)}
              height={Math.max(node.h - 4, 2)}
              rx={4}
              ry={4}
              fill={getColor(node.name)}
              stroke={hovered?.id === node.id ? '#fff' : 'none'}
              strokeWidth={1}
              onMouseMove={(e) => handleMouseMove(e, node)}
              style={{ filter: `drop-shadow(0 0 6px ${getColor(node.name)}60)` }}
            />
            {node.w > 40 && (
              <text
                x={node.x + 4}
                y={node.y + ROW_HEIGHT / 2 + 4}
                fill="#fff"
                fontSize={11}
                style={{ pointerEvents: 'none' }}
              >
                {node.name.length > 35 ? node.name.slice(0, 32) + '…' : node.name}
              </text>
            )}
          </g>
        ))}
      </svg>
      {hovered && (
        <div
          className="flame-graph-tooltip"
          style={{ left: tooltipPos.x + 12, top: tooltipPos.y + 8, position: 'fixed' }}
        >
          <div className="flame-graph-tooltip-name">{hovered.name}</div>
          <div className="flame-graph-tooltip-value">{formatValue(hovered.value, data.unit, data.valueType)}</div>
        </div>
      )}
    </div>
  );
}
