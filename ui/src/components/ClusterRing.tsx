import React, { useMemo } from "react";
import { useClusterStore } from "../store";
import { Role } from "../types";
import type { NodeID } from "../types";

interface Point {
  x: number;
  y: number;
}

const VIEW_BOX_SIZE = 640;
const CENTER_X = VIEW_BOX_SIZE / 2;
const CENTER_Y = VIEW_BOX_SIZE / 2;
const RING_RADIUS = 210;
const NODE_RADIUS = 42;

export const ClusterRing: React.FC = () => {
  const cluster = useClusterStore((s) => s.cluster);
  const selectedNodeId = useClusterStore((s) => s.selectedNodeId);
  const setSelectedNodeId = useClusterStore((s) => s.setSelectedNodeId);

  // Compute node IDs (1..5 or based on cluster keys)
  const nodeIds: NodeID[] = useMemo(() => {
    const keys = Object.keys(cluster.nodes).map(Number);
    return keys.length > 0 ? keys.sort((a, b) => a - b) : [1, 2, 3, 4, 5];
  }, [cluster.nodes]);

  // Node positions along the circle
  const nodePositions: Record<NodeID, Point> = useMemo(() => {
    const pos: Record<NodeID, Point> = {};
    const n = nodeIds.length;
    nodeIds.forEach((id, index) => {
      // Start at 12 o'clock (-pi/2) and distribute evenly
      const angle = -Math.PI / 2 + (2 * Math.PI * index) / n;
      pos[id] = {
        x: CENTER_X + RING_RADIUS * Math.cos(angle),
        y: CENTER_Y + RING_RADIUS * Math.sin(angle),
      };
    });
    return pos;
  }, [nodeIds]);

  // Mesh lines between all pairs of nodes
  const networkLines = useMemo(() => {
    const lines: Array<{ from: NodeID; to: NodeID; p1: Point; p2: Point }> = [];
    for (let i = 0; i < nodeIds.length; i++) {
      for (let j = i + 1; j < nodeIds.length; j++) {
        const from = nodeIds[i];
        const to = nodeIds[j];
        if (nodePositions[from] && nodePositions[to]) {
          lines.push({ from, to, p1: nodePositions[from], p2: nodePositions[to] });
        }
      }
    }
    return lines;
  }, [nodeIds, nodePositions]);

  // Inflight messages list
  const inflightMessages = useMemo(() => {
    return Object.values(cluster.inflight);
  }, [cluster.inflight]);

  const getRoleBadge = (role: Role) => {
    switch (role) {
      case Role.Leader:
        return { label: "LEADER", color: "#f59e0b", bg: "#78350f" };
      case Role.Candidate:
        return { label: "CANDIDATE", color: "#a855f7", bg: "#581c87" };
      case Role.PreCandidate:
        return { label: "PRE-CAND", color: "#6366f1", bg: "#312e81" };
      case Role.Follower:
      default:
        return { label: "FOLLOWER", color: "#38bdf8", bg: "#0c4a6e" };
    }
  };

  const getPacketColor = (type: string) => {
    switch (type) {
      case "App":
        return "#38bdf8"; // Sky cyan
      case "AppResp":
        return "#10b981"; // Emerald green
      case "Vote":
        return "#f59e0b"; // Amber
      case "VoteResp":
        return "#f97316"; // Orange
      case "PreVote":
        return "#8b5cf6"; // Violet
      case "PreVoteResp":
        return "#c084fc"; // Purple
      default:
        return "#e2e8f0";
    }
  };

  return (
    <div className="relative flex flex-col items-center justify-center p-4">
      <svg
        viewBox={`0 0 ${VIEW_BOX_SIZE} ${VIEW_BOX_SIZE}`}
        className="w-full max-w-[620px] h-auto select-none overflow-visible filter drop-shadow-2xl"
      >
        <defs>
          {/* Subtle glow filter */}
          <filter id="glow-gold" x="-20%" y="-20%" width="140%" height="140%">
            <feGaussianBlur stdDeviation="6" result="blur" />
            <feComposite in="SourceGraphic" in2="blur" operator="over" />
          </filter>
          <filter id="packet-glow" x="-40%" y="-40%" width="180%" height="180%">
            <feGaussianBlur stdDeviation="3.5" result="blur" />
            <feComposite in="SourceGraphic" in2="blur" operator="over" />
          </filter>
          <linearGradient id="ring-gradient" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stopColor="#3b82f6" stopOpacity="0.25" />
            <stop offset="50%" stopColor="#8b5cf6" stopOpacity="0.15" />
            <stop offset="100%" stopColor="#ec4899" stopOpacity="0.2" />
          </linearGradient>
        </defs>

        {/* Central circular guide ring */}
        <circle
          cx={CENTER_X}
          cy={CENTER_Y}
          r={RING_RADIUS}
          fill="none"
          stroke="url(#ring-gradient)"
          strokeWidth="1.5"
          strokeDasharray="6 8"
          className="animate-[spin_120s_linear_infinite] origin-center"
        />

        {/* Network connection mesh lines between all nodes */}
        <g className="network-mesh">
          {networkLines.map(({ from, to, p1, p2 }) => {
            const hasTraffic = inflightMessages.some(
              (m) =>
                (m.from === from && m.to === to) ||
                (m.from === to && m.to === from)
            );

            return (
              <line
                key={`line-${from}-${to}`}
                x1={p1.x}
                y1={p1.y}
                x2={p2.x}
                y2={p2.y}
                stroke={hasTraffic ? "#60a5fa" : "#334155"}
                strokeWidth={hasTraffic ? 2 : 1}
                strokeDasharray={hasTraffic ? "none" : "4 4"}
                strokeOpacity={hasTraffic ? 0.75 : 0.35}
                className="transition-all duration-300"
              />
            );
          })}
        </g>

        {/* Inflight Packet circles */}
        <g className="inflight-packets">
          {inflightMessages.map((msg) => {
            const pFrom = nodePositions[msg.from];
            const pTo = nodePositions[msg.to];
            if (!pFrom || !pTo) return null;

            // Interpolate packet position along line
            // Calculate a progressive offset based on elapsed virtual time
            const elapsed = Math.max(0, cluster.time - msg.sendTime);
            const progress = Math.min(0.85, Math.max(0.15, 0.15 + (elapsed / 25) * 0.7));
            const px = pFrom.x + (pTo.x - pFrom.x) * progress;
            const py = pFrom.y + (pTo.y - pFrom.y) * progress;
            const packetColor = getPacketColor(msg.type);

            return (
              <g key={`pkt-${msg.id}`} className="transition-all duration-150">
                {/* Glow ring */}
                <circle
                  cx={px}
                  cy={py}
                  r="10"
                  fill={packetColor}
                  fillOpacity="0.3"
                  filter="url(#packet-glow)"
                />
                {/* Solid packet center */}
                <circle
                  cx={px}
                  cy={py}
                  r="6"
                  fill={packetColor}
                  stroke="#0f172a"
                  strokeWidth="1.5"
                />
                {/* Message type tag */}
                <rect
                  x={px + 8}
                  y={py - 14}
                  width={msg.type.length * 6.5 + 8}
                  height="16"
                  rx="4"
                  fill="#0f172a"
                  fillOpacity="0.85"
                  stroke={packetColor}
                  strokeWidth="0.75"
                />
                <text
                  x={px + 12}
                  y={py - 2.5}
                  fill="#f8fafc"
                  fontSize="9.5"
                  fontWeight="600"
                  fontFamily="ui-monospace, monospace"
                >
                  {msg.type}
                </text>
              </g>
            );
          })}
        </g>

        {/* Nodes in the cluster */}
        <g className="cluster-nodes">
          {nodeIds.map((id) => {
            const node = cluster.nodes[id];
            const pos = nodePositions[id];
            if (!pos) return null;

            const isUp = node?.up ?? true;
            const role = node?.role ?? Role.Follower;
            const isLeader = role === Role.Leader && isUp;
            const isSelected = selectedNodeId === id;
            const badge = getRoleBadge(role);

            return (
              <g
                key={`node-${id}`}
                transform={`translate(${pos.x}, ${pos.y})`}
                onClick={() => setSelectedNodeId(isSelected ? null : id)}
                className="cursor-pointer group"
              >
                {/* Leader Crown and Glow Auras */}
                {isLeader && (
                  <>
                    <circle
                      r={NODE_RADIUS + 9}
                      fill="none"
                      stroke="#f59e0b"
                      strokeWidth="2.5"
                      strokeDasharray="4 3"
                      className="animate-[spin_10s_linear_infinite] origin-center opacity-75"
                    />
                    <circle
                      r={NODE_RADIUS + 4}
                      fill="#f59e0b"
                      fillOpacity="0.12"
                      filter="url(#glow-gold)"
                    />
                    {/* Crown Graphic atop Node */}
                    <g transform={`translate(0, ${-NODE_RADIUS - 16})`}>
                      <path
                        d="M -13 8 L -16 -4 L -7 2 L 0 -8 L 7 2 L 16 -4 L 13 8 Z"
                        fill="#f59e0b"
                        stroke="#78350f"
                        strokeWidth="1.2"
                      />
                      <circle cx="0" cy="-8" r="1.5" fill="#fef3c7" />
                      <circle cx="-16" cy="-4" r="1.5" fill="#fef3c7" />
                      <circle cx="16" cy="-4" r="1.5" fill="#fef3c7" />
                    </g>
                  </>
                )}

                {/* Candidate Auras */}
                {role === Role.Candidate && isUp && (
                  <circle
                    r={NODE_RADIUS + 6}
                    fill="none"
                    stroke="#a855f7"
                    strokeWidth="2"
                    strokeDasharray="5 3"
                    className="animate-[spin_6s_linear_infinite] origin-center opacity-65"
                  />
                )}

                {/* Node Base Circle */}
                <circle
                  r={NODE_RADIUS}
                  fill={isUp ? "#1e293b" : "#0f172a"}
                  fillOpacity={isUp ? 0.95 : 0.6}
                  stroke={
                    !isUp
                      ? "#475569"
                      : isSelected
                      ? "#38bdf8"
                      : isLeader
                      ? "#f59e0b"
                      : role === Role.Candidate
                      ? "#a855f7"
                      : "#334155"
                  }
                  strokeWidth={isSelected ? 3.5 : isLeader ? 3 : 2}
                  className="transition-all duration-300 group-hover:stroke-sky-400"
                />

                {/* Node Content when UP */}
                {isUp ? (
                  <>
                    {/* Node ID */}
                    <text
                      y="-12"
                      textAnchor="middle"
                      fill="#f8fafc"
                      fontSize="17"
                      fontWeight="700"
                      fontFamily="system-ui, sans-serif"
                    >
                      Node {id}
                    </text>

                    {/* Role Pill */}
                    <rect
                      x="-31"
                      y="-4"
                      width="62"
                      height="15"
                      rx="4"
                      fill={badge.bg}
                      stroke={badge.color}
                      strokeWidth="0.75"
                    />
                    <text
                      y="7"
                      textAnchor="middle"
                      fill={badge.color}
                      fontSize="8.5"
                      fontWeight="700"
                      fontFamily="system-ui, sans-serif"
                      letterSpacing="0.5"
                    >
                      {badge.label}
                    </text>

                    {/* Term & Commit Indices */}
                    <text
                      y="23"
                      textAnchor="middle"
                      fill="#94a3b8"
                      fontSize="10"
                      fontWeight="500"
                      fontFamily="ui-monospace, monospace"
                    >
                      T:{node?.term ?? 0} • C:{node?.commit ?? 0}
                    </text>

                    {/* Log count indicator */}
                    <text
                      y="34"
                      textAnchor="middle"
                      fill="#64748b"
                      fontSize="9"
                      fontFamily="ui-monospace, monospace"
                    >
                      Log:{node?.last ?? 0}
                    </text>
                  </>
                ) : (
                  /* Node Content when DOWN (Visible 'X' & Greyed out) */
                  <>
                    {/* Large prominent Red 'X' */}
                    <line
                      x1={-NODE_RADIUS * 0.58}
                      y1={-NODE_RADIUS * 0.58}
                      x2={NODE_RADIUS * 0.58}
                      y2={NODE_RADIUS * 0.58}
                      stroke="#ef4444"
                      strokeWidth="4.5"
                      strokeLinecap="round"
                    />
                    <line
                      x1={NODE_RADIUS * 0.58}
                      y1={-NODE_RADIUS * 0.58}
                      x2={-NODE_RADIUS * 0.58}
                      y2={NODE_RADIUS * 0.58}
                      stroke="#ef4444"
                      strokeWidth="4.5"
                      strokeLinecap="round"
                    />

                    {/* Node ID and OFFLINE badge */}
                    <rect
                      x="-25"
                      y="-9"
                      width="50"
                      height="18"
                      rx="3"
                      fill="#0f172a"
                      stroke="#ef4444"
                      strokeWidth="1"
                    />
                    <text
                      y="4"
                      textAnchor="middle"
                      fill="#ef4444"
                      fontSize="10"
                      fontWeight="800"
                      fontFamily="system-ui, sans-serif"
                    >
                      OFFLINE
                    </text>
                    <text
                      y="32"
                      textAnchor="middle"
                      fill="#64748b"
                      fontSize="10"
                      fontWeight="600"
                    >
                      Node {id}
                    </text>
                  </>
                )}
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
};
