import { useEffect, useRef, useState, useMemo } from "react";
import {
  Play,
  Pause,
  SkipForward,
  SkipBack,
  RotateCcw,
  Upload,
  Server,
  Layers,
  Clock,
  Radio,
  FileText,
  Activity,
  Zap,
  PowerOff,
  RefreshCw,
  ShieldAlert,
  HeartPulse,
  SlidersHorizontal,
} from "lucide-react";
import { isSessionText, useClusterStore } from "./store";
import { useSimReady } from "./useSimReady";
import { ClusterRing } from "./components/ClusterRing";
import { LiveControls } from "./components/LiveControls";
import { Role } from "./types";
import type { EventKind } from "./types";

export function App() {
  const {
    cluster,
    events,
    cursor,
    isPlaying,
    isLive,
    playbackSpeed,
    header,
    selectedNodeId,
    loadTrace,
    startLiveSession,
    sendCommand,
    replaySession,
    commandError,
    clearCommandError,
    goLive,
    stepForward,
    stepBackward,
    seekTo,
    play,
    pause,
    setPlaybackSpeed,
    setSelectedNodeId,
    resetCluster,
  } = useClusterStore();

  const [isLoading, setIsLoading] = useState(false);
  const [activeTab, setActiveTab] = useState<"inspector" | "events" | "network" | "controls">("inspector");
  const simReady = useSimReady();
  const behindLive = isLive && cursor < events.length;
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Auto-load default seed 182 trace from public/trace_seed182.ndjson on initial mount
  useEffect(() => {
    async function loadDefaultTrace() {
      setIsLoading(true);
      try {
        const res = await fetch("./trace_seed182.ndjson");
        if (res.ok) {
          const text = await res.text();
          loadTrace(text);
        }
      } catch (err) {
        console.warn("Could not load default trace:", err);
      } finally {
        setIsLoading(false);
      }
    }
    loadDefaultTrace();
  }, [loadTrace]);

  // Auto-play interval
  useEffect(() => {
    if (!isPlaying) return;
    const interval = setInterval(() => {
      stepForward();
    }, playbackSpeed);
    return () => clearInterval(interval);
  }, [isPlaying, playbackSpeed, stepForward]);

  // Handle custom file upload
  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const content = event.target?.result as string;
      if (!content) return;
      // A downloaded session is replayed in the simulator; anything else is an NDJSON trace.
      if (isSessionText(content)) {
        if (!replaySession(content)) setActiveTab("controls");
      } else {
        loadTrace(content);
      }
    };
    reader.readAsText(file);
    e.target.value = ""; // allow loading the same file twice
  };

  // Find current leader
  const leaderId = useMemo(() => {
    for (const id in cluster.nodes) {
      const n = cluster.nodes[id];
      if (n.role === Role.Leader && n.up) {
        return n.id;
      }
    }
    return null;
  }, [cluster.nodes]);

  // Selected node (defaults to leader or node 1)
  const inspectedNode = useMemo(() => {
    const id = selectedNodeId ?? leaderId ?? 1;
    return cluster.nodes[id];
  }, [selectedNodeId, leaderId, cluster.nodes]);

  // Recent events around cursor
  const recentEvents = useMemo(() => {
    const start = Math.max(0, cursor - 15);
    const end = Math.min(events.length, cursor + 5);
    return events.slice(start, end).map((ev, idx) => ({
      ...ev,
      globalIndex: start + idx,
      isCurrent: start + idx === cursor - 1,
    }));
  }, [events, cursor]);

  const getEventBadge = (kind: EventKind) => {
    switch (kind) {
      case "send":
        return { label: "SEND", bg: "bg-blue-900/60 text-blue-300 border-blue-700" };
      case "deliver":
        return { label: "DELIVER", bg: "bg-emerald-900/60 text-emerald-300 border-emerald-700" };
      case "drop":
        return { label: "DROP", bg: "bg-rose-900/60 text-rose-300 border-rose-700" };
      case "fault":
        return { label: "FAULT", bg: "bg-amber-900/60 text-amber-300 border-amber-700" };
      case "node":
        return { label: "NODE", bg: "bg-purple-900/60 text-purple-300 border-purple-700" };
      case "log":
        return { label: "LOG", bg: "bg-cyan-900/60 text-cyan-300 border-cyan-700" };
      case "propose":
        return { label: "PROPOSE", bg: "bg-teal-900/60 text-teal-300 border-teal-700" };
      case "net":
        return { label: "NET", bg: "bg-violet-900/60 text-violet-300 border-violet-700" };
      case "violation":
        return { label: "VIOLATION", bg: "bg-red-950 text-red-400 border-red-500" };
      default:
        return { label: kind, bg: "bg-slate-800 text-slate-300 border-slate-700" };
    }
  };

  const inflightList = useMemo(() => Object.values(cluster.inflight), [cluster.inflight]);

  return (
    <div className="flex flex-col min-h-screen bg-slate-950 text-slate-100 font-sans selection:bg-sky-500 selection:text-white">
      {/* Top Navigation Bar */}
      <header className="flex items-center justify-between px-6 py-3.5 bg-slate-900/90 backdrop-blur border-b border-slate-800/80 sticky top-0 z-50">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-sky-500 to-indigo-600 flex items-center justify-center shadow-lg shadow-sky-500/20">
            <Radio className="w-5 h-5 text-white animate-pulse" />
          </div>
          <div>
            <h1 className="text-base font-bold tracking-tight bg-gradient-to-r from-white via-slate-200 to-slate-400 bg-clip-text text-transparent">
              Raft Consensus Trace Visualizer
            </h1>
            <p className="text-xs text-slate-400 flex items-center gap-2">
              <span>M7b Protocol Inspection</span>
              <span>•</span>
              <span className="text-sky-400">Seed {header?.seed ?? 182}</span>
            </p>
          </div>
        </div>

        {/* Global Cluster Stats Badges */}
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-800/70 border border-slate-700/60 text-xs">
            <Clock className="w-3.5 h-3.5 text-sky-400" />
            <span className="text-slate-400">Virtual Time:</span>
            <span className="font-mono font-bold text-sky-300">{cluster.time} ms</span>
          </div>

          <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-800/70 border border-slate-700/60 text-xs">
            <Server className="w-3.5 h-3.5 text-amber-400" />
            <span className="text-slate-400">Leader:</span>
            <span className="font-mono font-bold text-amber-300">
              {leaderId ? `Node ${leaderId}` : "None (Election)"}
            </span>
          </div>

          <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-800/70 border border-slate-700/60 text-xs">
            <Activity className="w-3.5 h-3.5 text-emerald-400" />
            <span className="text-slate-400">Inflight:</span>
            <span className="font-mono font-bold text-emerald-300">{inflightList.length} pkts</span>
          </div>

          {/* Start Live Session Button */}
          <button
            onClick={() => startLiveSession(header?.seed ?? 182)}
            disabled={!simReady}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold border transition-all shadow-md active:scale-95 ${
              isLive
                ? "bg-rose-600/90 hover:bg-rose-600 text-white border-rose-500 shadow-rose-600/25 ring-2 ring-rose-500/30"
                : "bg-sky-600 hover:bg-sky-500 text-white border-sky-500 shadow-sky-600/25"
            }`}
            title={simReady ? "Start live Go WebAssembly Raft simulation" : "Loading sim.wasm…"}
          >
            <Zap className={`w-3.5 h-3.5 ${isLive ? "fill-white animate-bounce" : ""}`} />
            <span>{isLive ? "Restart Live Session" : simReady ? "Start Live Session" : "Loading simulator…"}</span>
          </button>

          {/* Global Heal Network Button */}
          <button
            onClick={() => sendCommand({ kind: "heal" })}
            disabled={!isLive}
            className="disabled:opacity-40 flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-emerald-600/80 hover:bg-emerald-600 text-white text-xs font-medium border border-emerald-500/60 transition-all shadow-md shadow-emerald-600/20 active:scale-95"
            title="Heal all partitions in the cluster"
          >
            <HeartPulse className="w-3.5 h-3.5" />
            <span>Heal Network</span>
          </button>

          {/* Load Trace File Button */}
          <input
            type="file"
            ref={fileInputRef}
            onChange={handleFileUpload}
            accept=".ndjson,.golden,.txt,.json"
            className="hidden"
          />
          <button
            onClick={() => fileInputRef.current?.click()}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-white text-xs font-medium border border-slate-700 transition-all active:scale-95"
            title="Load an NDJSON trace, or a downloaded session to replay"
          >
            <Upload className="w-3.5 h-3.5" />
            <span>Load</span>
          </button>
        </div>
      </header>

      {commandError && (
        <div
          role="alert"
          className="flex items-center justify-between px-6 py-2 bg-rose-950/80 border-b border-rose-800 text-xs text-rose-200"
        >
          <span>{commandError}</span>
          <button onClick={clearCommandError} className="text-rose-300 hover:text-white font-bold px-2">
            ✕
          </button>
        </div>
      )}

      {/* Main Studio Viewport */}
      <main className="flex-1 grid grid-cols-1 lg:grid-cols-12 gap-0 overflow-hidden">
        {/* Left Column: Visualizer Canvas & Playback Controls */}
        <section className="lg:col-span-7 xl:col-span-8 flex flex-col justify-between border-r border-slate-800/80 bg-gradient-to-b from-slate-950 via-slate-900/40 to-slate-950 p-6 relative">
          {/* Subtle Background Grid */}
          <div
            className="absolute inset-0 opacity-[0.03] pointer-events-none"
            style={{
              backgroundImage:
                "radial-gradient(#94a3b8 1px, transparent 1px), radial-gradient(#94a3b8 1px, #020617 1px)",
              backgroundSize: "24px 24px",
            }}
          />

          {/* Cluster Ring Canvas */}
          <div className="flex-1 flex flex-col items-center justify-center relative z-10">
            {isLoading ? (
              <div className="flex flex-col items-center gap-3 text-slate-400 py-16">
                <div className="w-8 h-8 border-2 border-sky-500 border-t-transparent rounded-full animate-spin" />
                <span className="text-xs font-mono">Loading trace stream...</span>
              </div>
            ) : (
              <ClusterRing />
            )}
          </div>

          {/* Bottom Interactive Playback Console */}
          <div className="w-full max-w-2xl mx-auto bg-slate-900/90 backdrop-blur-md rounded-2xl border border-slate-800/90 p-4 shadow-2xl z-20 space-y-3.5">
            {/* Timeline Slider with Cursor Counter */}
            <div className="space-y-1.5">
              <div className="flex justify-between text-xs text-slate-400">
                <span className="font-mono flex items-center gap-2">
                  Step {cursor} of {events.length}
                  {isLive && (
                    <button
                      onClick={goLive}
                      disabled={!behindLive}
                      className={`px-2 py-0.5 rounded text-[10px] font-bold border ${
                        behindLive
                          ? "bg-sky-600 border-sky-400 text-white hover:bg-sky-500"
                          : "bg-rose-950/60 border-rose-700 text-rose-300"
                      }`}
                      title={behindLive ? "Jump to the live edge" : "Following the live simulation"}
                    >
                      {behindLive ? "Jump to live" : "● LIVE"}
                    </button>
                  )}
                </span>
                <span className="font-mono">
                  {events.length > 0
                    ? `${Math.round((cursor / events.length) * 100)}%`
                    : "0%"}
                </span>
              </div>
              <input
                type="range"
                min={0}
                max={events.length}
                value={cursor}
                onChange={(e) => seekTo(Number(e.target.value))}
                className="w-full h-1.5 bg-slate-800 rounded-lg appearance-none cursor-pointer accent-sky-400 hover:accent-sky-300 transition-all"
              />
            </div>

            {/* Transport Toolbar */}
            <div className="flex items-center justify-between pt-1">
              {/* Reset & Step Backwards */}
              <div className="flex items-center gap-2">
                <button
                  onClick={() => resetCluster(header?.nodes ?? [1, 2, 3, 4, 5])}
                  className="p-2 rounded-lg bg-slate-800/80 hover:bg-slate-800 text-slate-300 hover:text-white border border-slate-700/60 transition-all active:scale-95"
                  title="Reset to initial state"
                >
                  <RotateCcw className="w-4 h-4" />
                </button>
                <button
                  onClick={stepBackward}
                  disabled={cursor === 0}
                  className="p-2 rounded-lg bg-slate-800/80 hover:bg-slate-800 disabled:opacity-40 text-slate-300 hover:text-white border border-slate-700/60 transition-all active:scale-95"
                  title="Step Backward"
                >
                  <SkipBack className="w-4 h-4" />
                </button>
              </div>

              {/* Play / Pause Toggle */}
              <button
                onClick={isPlaying ? pause : play}
                disabled={events.length === 0}
                className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-400 hover:to-blue-500 text-white font-semibold text-sm shadow-lg shadow-sky-500/25 transition-all active:scale-95"
              >
                {isPlaying ? (
                  <>
                    <Pause className="w-4 h-4 fill-white" />
                    <span>Pause</span>
                  </>
                ) : (
                  <>
                    <Play className="w-4 h-4 fill-white" />
                    <span>Play Trace</span>
                  </>
                )}
              </button>

              {/* Step Forward & Speed Controls */}
              <div className="flex items-center gap-2">
                <button
                  onClick={stepForward}
                  disabled={cursor >= events.length}
                  className="p-2 rounded-lg bg-slate-800/80 hover:bg-slate-800 disabled:opacity-40 text-slate-300 hover:text-white border border-slate-700/60 transition-all active:scale-95"
                  title="Step Forward"
                >
                  <SkipForward className="w-4 h-4" />
                </button>

                {/* Speed Selector */}
                <div
                  title="Trace playback speed (not the live simulation speed — see the Controls tab)"
                  className="flex items-center rounded-lg bg-slate-800/80 border border-slate-700/60 text-xs overflow-hidden"
                >
                  {[
                    { label: "0.5x", val: 100 },
                    { label: "1x", val: 50 },
                    { label: "2x", val: 20 },
                    { label: "5x", val: 5 },
                  ].map((speed) => (
                    <button
                      key={speed.label}
                      onClick={() => setPlaybackSpeed(speed.val)}
                      className={`px-2 py-1.5 transition-colors ${
                        playbackSpeed === speed.val
                          ? "bg-sky-500 text-white font-semibold"
                          : "text-slate-400 hover:text-white"
                      }`}
                    >
                      {speed.label}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Right Column: Deep Inspection Panel */}
        <aside className="lg:col-span-5 xl:col-span-4 bg-slate-900/60 backdrop-blur border-l border-slate-800/80 flex flex-col h-full">
          {/* Navigation Tabs */}
          <div className="flex items-center border-b border-slate-800/80 px-4 pt-3 gap-2">
            <button
              onClick={() => setActiveTab("inspector")}
              className={`flex items-center gap-2 px-3 py-2 text-xs font-medium border-b-2 transition-all ${
                activeTab === "inspector"
                  ? "border-sky-400 text-sky-400"
                  : "border-transparent text-slate-400 hover:text-slate-200"
              }`}
            >
              <Server className="w-3.5 h-3.5" />
              <span>Node Inspector</span>
            </button>

            <button
              onClick={() => setActiveTab("events")}
              className={`flex items-center gap-2 px-3 py-2 text-xs font-medium border-b-2 transition-all ${
                activeTab === "events"
                  ? "border-sky-400 text-sky-400"
                  : "border-transparent text-slate-400 hover:text-slate-200"
              }`}
            >
              <FileText className="w-3.5 h-3.5" />
              <span>Event Log</span>
            </button>

            <button
              onClick={() => setActiveTab("network")}
              className={`flex items-center gap-2 px-3 py-2 text-xs font-medium border-b-2 transition-all ${
                activeTab === "network"
                  ? "border-sky-400 text-sky-400"
                  : "border-transparent text-slate-400 hover:text-slate-200"
              }`}
            >
              <Layers className="w-3.5 h-3.5" />
              <span>Inflight ({inflightList.length})</span>
            </button>

            <button
              onClick={() => setActiveTab("controls")}
              className={`flex items-center gap-2 px-3 py-2 text-xs font-medium border-b-2 transition-all ${
                activeTab === "controls"
                  ? "border-sky-400 text-sky-400"
                  : "border-transparent text-slate-400 hover:text-slate-200"
              }`}
            >
              <SlidersHorizontal className="w-3.5 h-3.5" />
              <span>Controls</span>
            </button>
          </div>

          {/* Tab Content Body */}
          <div className="flex-1 overflow-y-auto p-5 space-y-4">
            {activeTab === "inspector" && inspectedNode && (
              <div className="space-y-4">
                {/* Node Selector Pills */}
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400 uppercase tracking-wider font-semibold">
                    Select Server:
                  </span>
                  <div className="flex gap-1.5">
                    {Object.keys(cluster.nodes)
                      .map(Number)
                      .map((id) => (
                        <button
                          key={`sel-${id}`}
                          onClick={() => setSelectedNodeId(id)}
                          className={`w-7 h-7 rounded-lg text-xs font-bold transition-all ${
                            inspectedNode.id === id
                              ? "bg-sky-500 text-white shadow-md shadow-sky-500/20"
                              : "bg-slate-800 text-slate-400 hover:text-white"
                          }`}
                        >
                          {id}
                        </button>
                      ))}
                  </div>
                </div>

                {/* Node Card Overview */}
                <div className="p-4 rounded-xl bg-slate-800/60 border border-slate-700/60 space-y-3">
                  <div className="flex items-center justify-between">
                    <div>
                      <h3 className="text-base font-bold text-white flex items-center gap-2">
                        <span>Node {inspectedNode.id}</span>
                        {inspectedNode.up ? (
                          <span className="inline-block w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                        ) : (
                          <span className="inline-block w-2 h-2 rounded-full bg-rose-500" />
                        )}
                      </h3>
                      <span className="text-xs text-slate-400">
                        Status:{" "}
                        <strong className={inspectedNode.up ? "text-emerald-400" : "text-rose-400"}>
                          {inspectedNode.up ? "ONLINE" : "CRASHED / OFFLINE"}
                        </strong>
                      </span>
                    </div>

                    <div className="text-right">
                      <span
                        className={`text-xs font-bold px-2.5 py-1 rounded-md border ${
                          inspectedNode.role === Role.Leader
                            ? "bg-amber-950/60 border-amber-600 text-amber-300"
                            : inspectedNode.role === Role.Candidate
                            ? "bg-purple-950/60 border-purple-600 text-purple-300"
                            : "bg-sky-950/60 border-sky-600 text-sky-300"
                        }`}
                      >
                        {inspectedNode.role === Role.Leader
                          ? "LEADER"
                          : inspectedNode.role === Role.Candidate
                          ? "CANDIDATE"
                          : inspectedNode.role === Role.PreCandidate
                          ? "PRE-CANDIDATE"
                          : "FOLLOWER"}
                      </span>
                    </div>
                  </div>

                  {/* Metrics Grid */}
                  <div className="grid grid-cols-4 gap-2 pt-2 border-t border-slate-700/50 text-center font-mono text-xs">
                    <div className="p-2 rounded-lg bg-slate-900/60">
                      <div className="text-slate-400 text-[10px]">TERM</div>
                      <div className="text-white font-bold text-sm">{inspectedNode.term}</div>
                    </div>
                    <div className="p-2 rounded-lg bg-slate-900/60">
                      <div className="text-slate-400 text-[10px]">COMMIT</div>
                      <div className="text-emerald-300 font-bold text-sm">
                        {inspectedNode.commit}
                      </div>
                    </div>
                    <div className="p-2 rounded-lg bg-slate-900/60">
                      <div className="text-slate-400 text-[10px]">APPLIED</div>
                      <div className="text-sky-300 font-bold text-sm">
                        {inspectedNode.applied}
                      </div>
                    </div>
                    <div className="p-2 rounded-lg bg-slate-900/60">
                      <div className="text-slate-400 text-[10px]">LOG LEN</div>
                      <div className="text-amber-300 font-bold text-sm">
                        {inspectedNode.log.length}
                      </div>
                    </div>
                  </div>

                  {/* Interactive Fault Injection Controls */}
                  <div className="pt-3 border-t border-slate-700/50 space-y-2">
                    <div className="flex items-center justify-between">
                      <span className="text-[10px] text-slate-400 uppercase tracking-wider font-semibold">
                        Fault Controls (Node {inspectedNode.id})
                      </span>
                      <span className="text-[10px] text-slate-500 font-mono">
                        {inspectedNode.up ? "Online" : "Offline"}
                      </span>
                    </div>
                    <div className="grid grid-cols-3 gap-2">
                      <button
                        onClick={() => sendCommand({ kind: "crash", node: inspectedNode.id })}
                        disabled={!isLive || !inspectedNode.up}
                        className="flex items-center justify-center gap-1.5 py-2 px-2.5 rounded-lg bg-rose-950/70 hover:bg-rose-900 disabled:opacity-40 disabled:hover:bg-rose-950/70 text-rose-300 font-medium text-xs border border-rose-800/60 transition-all active:scale-95 shadow-sm"
                        title={`Crash Node ${inspectedNode.id}`}
                      >
                        <PowerOff className="w-3.5 h-3.5" />
                        <span>Crash</span>
                      </button>

                      <button
                        onClick={() => sendCommand({ kind: "restart", node: inspectedNode.id })}
                        disabled={!isLive || inspectedNode.up}
                        className="flex items-center justify-center gap-1.5 py-2 px-2.5 rounded-lg bg-emerald-950/70 hover:bg-emerald-900 disabled:opacity-40 disabled:hover:bg-emerald-950/70 text-emerald-300 font-medium text-xs border border-emerald-800/60 transition-all active:scale-95 shadow-sm"
                        title={`Restart Node ${inspectedNode.id}`}
                      >
                        <RefreshCw className="w-3.5 h-3.5" />
                        <span>Restart</span>
                      </button>

                      <button
                        onClick={() => sendCommand({ kind: "isolate", node: inspectedNode.id })}
                        disabled={!isLive}
                        className="disabled:opacity-40 flex items-center justify-center gap-1.5 py-2 px-2.5 rounded-lg bg-amber-950/70 hover:bg-amber-900 text-amber-300 font-medium text-xs border border-amber-800/60 transition-all active:scale-95 shadow-sm"
                        title={`Isolate Node ${inspectedNode.id} from network`}
                      >
                        <ShieldAlert className="w-3.5 h-3.5" />
                        <span>Isolate</span>
                      </button>
                    </div>
                  </div>
                </div>

                {/* Replicated Log Entries Table */}
                <div className="space-y-2">
                  <div className="flex items-center justify-between text-xs text-slate-400 font-medium">
                    <span>Replicated Log ({inspectedNode.log.length} entries)</span>
                    <span className="font-mono text-[11px]">1-indexed</span>
                  </div>

                  {inspectedNode.log.length === 0 ? (
                    <div className="p-6 text-center text-xs text-slate-500 rounded-xl bg-slate-800/30 border border-slate-800">
                      No entries in local log yet.
                    </div>
                  ) : (
                    <div className="border border-slate-800 rounded-xl overflow-hidden max-h-72 overflow-y-auto">
                      <table className="w-full text-xs text-left font-mono">
                        <thead className="bg-slate-800/80 text-slate-400 text-[10px] uppercase sticky top-0">
                          <tr>
                            <th className="px-3 py-2">Index</th>
                            <th className="px-3 py-2">Term</th>
                            <th className="px-3 py-2">Status</th>
                            <th className="px-3 py-2">Payload</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-slate-800/60 bg-slate-900/40">
                          {inspectedNode.log.map((entry) => {
                            const isCommitted = entry.Index <= inspectedNode.commit;
                            return (
                              <tr
                                key={`log-${entry.Index}`}
                                className={isCommitted ? "hover:bg-slate-800/30" : "bg-slate-950/40 opacity-70"}
                              >
                                <td className="px-3 py-2 font-bold text-sky-400">
                                  #{entry.Index}
                                </td>
                                <td className="px-3 py-2 text-slate-300">T:{entry.Term}</td>
                                <td className="px-3 py-2">
                                  {isCommitted ? (
                                    <span className="text-[10px] text-emerald-400 font-semibold">
                                      COMMITTED
                                    </span>
                                  ) : (
                                    <span className="text-[10px] text-amber-400">
                                      UNCOMMITTED
                                    </span>
                                  )}
                                </td>
                                <td className="px-3 py-2 text-slate-400 truncate max-w-[120px]">
                                  {entry.Data ? entry.Data : "<no-op>"}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* Event Log Stream */}
            {activeTab === "events" && (
              <div className="space-y-2">
                <span className="text-xs text-slate-400 uppercase tracking-wider font-semibold">
                  Trace Window ({events.length} total events)
                </span>
                <div className="space-y-1.5 font-mono text-xs">
                  {recentEvents.map((ev) => {
                    const badge = getEventBadge(ev.ev);
                    return (
                      <div
                        key={`feed-${ev.globalIndex}`}
                        onClick={() => seekTo(ev.globalIndex + 1)}
                        className={`p-2 rounded-lg border transition-all cursor-pointer flex items-center justify-between ${
                          ev.isCurrent
                            ? "bg-sky-950/50 border-sky-500 shadow-md shadow-sky-500/10 text-white"
                            : "bg-slate-800/40 border-slate-800 text-slate-400 hover:bg-slate-800/70"
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          <span
                            className={`text-[9.5px] px-1.5 py-0.5 rounded border font-bold ${badge.bg}`}
                          >
                            {badge.label}
                          </span>
                          <span className="font-semibold text-slate-200">
                            {ev.ev === "send" && `${ev.from} ➔ ${ev.to} (${ev.type})`}
                            {ev.ev === "deliver" && `Delivered ${ev.from} ➔ ${ev.to}`}
                            {ev.ev === "drop" && `Dropped ${ev.from} ➔ ${ev.to} [${ev.why}]`}
                            {ev.ev === "fault" &&
                              (ev.from ? `Node ${ev.from} ${ev.why}` : `Network ${ev.why}`) +
                                (ev.why && ["cut", "isolate", "partition", "heal"].includes(ev.why)
                                  ? ` (${ev.cuts?.length ?? 0} links cut)`
                                  : "")}
                            {ev.ev === "propose" &&
                              `${ev.msg} → ${ev.why}${ev.from ? ` (node ${ev.from})` : ""}`}
                            {ev.ev === "net" &&
                              `loss ${Math.round((ev.net?.drop ?? 0) * 100)}% dup ${Math.round((ev.net?.dup ?? 0) * 100)}% delay ${ev.net?.minDelay}-${ev.net?.maxDelay}ms`}
                            {ev.ev === "node" && `Node ${ev.from} state update`}
                            {ev.ev === "log" && `Node ${ev.from} logged +${ev.entries?.length ?? 1} entries`}
                            {ev.ev === "violation" && `Safety Violation: ${ev.msg}`}
                          </span>
                        </div>
                        <span className="text-[10px] text-slate-500">t={ev.t}ms</span>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {activeTab === "controls" && <LiveControls />}

            {/* Network Inflight View */}
            {activeTab === "network" && (
              <div className="space-y-3">
                <span className="text-xs text-slate-400 uppercase tracking-wider font-semibold">
                  Messages In-Flight ({inflightList.length})
                </span>
                {inflightList.length === 0 ? (
                  <div className="p-8 text-center text-xs text-slate-500 rounded-xl bg-slate-800/30 border border-slate-800">
                    No active RPC packets in transit.
                  </div>
                ) : (
                  <div className="space-y-2">
                    {inflightList.map((m) => (
                      <div
                        key={`inflight-${m.id}`}
                        className="p-3 rounded-xl bg-slate-800/50 border border-slate-700/60 font-mono text-xs flex items-center justify-between"
                      >
                        <div>
                          <div className="font-bold text-sky-400">
                            Msg #{m.id}: {m.type}
                          </div>
                          <div className="text-slate-400 text-[11px]">
                            Node {m.from} ➔ Node {m.to} (Term {m.term})
                          </div>
                        </div>
                        <span className="text-[10px] text-slate-500">
                          sent at {m.sendTime}ms
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        </aside>
      </main>
    </div>
  );
}

export default App;
