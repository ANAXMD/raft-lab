export type NodeID = number;
export type Term = number;
export type Index = number;

/** The Go/WASM bridge (cmd/wasm). Strings returned by commands are error messages; "" means success. */
export interface RaftSimAPI {
  start: (seed: number, onEvent: (evJSON: string) => void, optionsJSON?: string) => void;
  sendCommand: (cmdJSON: string) => string;
  exportSession: () => string;
  replay: (sessionJSON: string, onEvent: (evJSON: string) => void) => string;
  setSpeed: (virtualSecondsPerWallSecond: number) => void;
}

declare global {
  interface Window {
    RaftSim?: RaftSimAPI;
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
  /** Plain text payload ("SET k=v"); null/absent for the leader's no-op entry. */
  Data?: string | null;
}

/** A cut link, always stored with the smaller node id first. */
export type Cut = [NodeID, NodeID];

/** The runtime-adjustable network model (wire names match sim.NetConfig). */
export interface NetConfig {
  drop: number; // probability a message is lost, 0..1
  dup: number; // probability a message is duplicated, 0..1
  minDelay: number; // ms, >= 1
  maxDelay: number; // ms, >= minDelay
}

export type FaultKind = "crash" | "restart" | "isolate" | "partition" | "cut" | "heal";
export type ProposeOutcome = "accepted" | "rejected" | "no-leader";

export type EventKind =
  | "send"
  | "deliver"
  | "drop"
  | "node"
  | "log"
  | "fault"
  | "propose"
  | "net"
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
  why?: string; // drop reason ("loss"|"partition"|"down"), FaultKind, or ProposeOutcome

  // Node snapshot fields (emitted only on changes)
  role?: Role;
  commit?: Index;
  last?: Index;
  up?: boolean;

  // Log fields (mirrors Ready.Entries behavior)
  idx?: Index;
  entries?: Entry[];

  // Network topology and settings
  cuts?: Cut[]; // network faults: the COMPLETE set of cut links; absent = none
  net?: NetConfig; // "net" events: settings now in force

  msg?: string; // violations, and the payload of "propose" events
}

/** A user command, as sent to RaftSim.sendCommand (wire names match sim.Command). */
export type Command =
  | { kind: "crash" | "restart" | "isolate"; node: NodeID }
  | { kind: "cut"; node: NodeID; peer: NodeID }
  | { kind: "partition"; groups: NodeID[][] }
  | { kind: "heal" }
  | { kind: "propose"; key: string; value: string; node?: NodeID }
  | ({ kind: "net" } & Partial<{ drop: number; dup: number; minDelay: number; maxDelay: number }>);

/** The recording produced by RaftSim.exportSession (sim.SessionFile). */
export interface SessionFile {
  v: number;
  seed: number;
  config: {
    n: number;
    tickMs: number;
    minDelayMs: number;
    maxDelayMs: number;
    dropProb: number;
    dupProb: number;
    electionTicks: number;
    heartbeatTicks: number;
    preVote: boolean;
    checkQuorum: boolean;
    skipSync: boolean;
  };
  commands: Array<{ t: number; kind: string } & Record<string, unknown>>;
  until: number;
}

/** Options accepted by RaftSim.start (sim.Config subset). */
export interface LiveOptions {
  n?: number;
  preVote?: boolean;
  checkQuorum?: boolean;
  skipSync?: boolean;
  dropProb?: number;
  dupProb?: number;
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

export interface ProposeNote {
  t: number;
  node: NodeID; // 0 when there was no leader to ask
  outcome: ProposeOutcome;
  data: string;
}

export interface ClusterState {
  time: number;
  nodes: Record<NodeID, NodeState>;
  inflight: Record<number, InflightMessage>;
  /** Links currently cut by a partition, isolation or manual cut. */
  cuts: Cut[];
  /** Network settings in force; null until the first "net" event (see store.netConfig). */
  net: NetConfig | null;
  /** The most recent propose attempt, so a replay shows it at the same moment. */
  lastPropose: ProposeNote | null;
}
