export type NodeID = number;
export type Term = number;
export type Index = number;

declare global {
  interface Window {
    RaftSim?: {
      start: (seed: number, onEvent: (evJSON: string) => void) => void;
      sendCommand: (cmdJSON: string) => void;
    };
    Go?: any;
  }
}

export const Role = {
  Follower: 0,
  PreCandidate: 1,
  Candidate: 2,
  Leader: 3,
} as const;
export type Role = (typeof Role)[keyof typeof Role];

export interface Entry {
  Term: Term;
  Index: Index;
  Data?: string | null;
}

export type EventKind =
  | "send"
  | "deliver"
  | "drop"
  | "node"
  | "log"
  | "fault"
  | "violation";

export interface Event {
  t: number; // virtual ms
  ev: EventKind;
  id?: number; // message id: links send -> deliver/drop
  from?: NodeID;
  to?: NodeID;
  type?: string;
  term?: Term;
  n?: number; // entry count for appends
  why?: string; // drop reason: "loss", "partition", "down" or fault why

  // Node snapshot fields (emitted only on changes)
  role?: Role;
  commit?: Index;
  last?: Index;
  up?: boolean;

  // Log fields (mirrors Ready.Entries behavior)
  idx?: Index;
  entries?: Entry[];

  msg?: string; // For violations or general text
}

export interface TraceHeader {
  v: number;
  seed: number;
  nodes: NodeID[];
}

export interface InflightMessage {
  id: number;
  from: NodeID;
  to: NodeID;
  type: string;
  term: Term;
  n?: number;
  sendTime: number;
}

export interface NodeState {
  id: NodeID;
  role: Role;
  term: Term;
  commit: Index;
  last: Index;
  applied: Index;
  up: boolean;
  log: Entry[];
}

export interface ClusterState {
  time: number;
  nodes: Record<NodeID, NodeState>;
  inflight: Record<number, InflightMessage>;
}
