import { useState } from "react";
import { Download, HeartPulse, Scissors, Send, SlidersHorizontal } from "lucide-react";
import { MAX_LIVE_SPEED, useClusterStore } from "../store";
import type { NetConfig, NodeID } from "../types";

const SPEEDS = [0, 0.25, 0.5, 1, 2, MAX_LIVE_SPEED];

const btn =
  "flex items-center justify-center gap-1.5 py-2 px-3 rounded-lg text-xs font-medium border transition-all active:scale-95 disabled:opacity-40 disabled:active:scale-100";
const field =
  "w-full px-2 py-1.5 rounded-lg bg-slate-900 border border-slate-700 text-xs font-mono text-slate-100 focus:outline-none focus:border-sky-500 disabled:opacity-40";
const label = "text-[10px] text-slate-400 uppercase tracking-wider font-semibold";

function Section({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="p-4 rounded-xl bg-slate-800/60 border border-slate-700/60 space-y-3">
      <h3 className="flex items-center gap-2 text-xs font-bold text-slate-200">
        {icon}
        <span>{title}</span>
      </h3>
      {children}
    </section>
  );
}

function NetworkSection({
  live,
  enabled,
  apply,
}: {
  live: NetConfig;
  enabled: boolean;
  apply: (n: NetConfig) => void;
}) {
  // Drafts: nothing reaches the simulator until "Apply", so one change = one recorded command.
  const [drop, setDrop] = useState(Math.round(live.drop * 100));
  const [dup, setDup] = useState(Math.round(live.dup * 100));
  const [minDelay, setMinDelay] = useState(live.minDelay);
  const [maxDelay, setMaxDelay] = useState(live.maxDelay);
  const dirty =
    drop !== Math.round(live.drop * 100) ||
    dup !== Math.round(live.dup * 100) ||
    minDelay !== live.minDelay ||
    maxDelay !== live.maxDelay;

  return (
    <Section title="Network" icon={<SlidersHorizontal className="w-3.5 h-3.5 text-violet-400" />}>
      <label className="block space-y-1">
        <span className={label}>Message loss: {drop}%</span>
        <input type="range" min={0} max={50} value={drop} disabled={!enabled}
          onChange={(e) => setDrop(Number(e.target.value))} className="w-full accent-rose-400" />
      </label>
      <label className="block space-y-1">
        <span className={label}>Duplication: {dup}%</span>
        <input type="range" min={0} max={50} value={dup} disabled={!enabled}
          onChange={(e) => setDup(Number(e.target.value))} className="w-full accent-amber-400" />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="space-y-1">
          <span className={label}>Min delay (ms)</span>
          <input type="number" min={1} className={field} value={minDelay} disabled={!enabled}
            onChange={(e) => setMinDelay(Number(e.target.value))} />
        </label>
        <label className="space-y-1">
          <span className={label}>Max delay (ms)</span>
          <input type="number" min={1} className={field} value={maxDelay} disabled={!enabled}
            onChange={(e) => setMaxDelay(Number(e.target.value))} />
        </label>
      </div>
      <button
        className={`${btn} w-full bg-violet-700/80 hover:bg-violet-600 text-white border-violet-500/60`}
        disabled={!enabled || !dirty}
        onClick={() => apply({ drop: drop / 100, dup: dup / 100, minDelay, maxDelay })}
      >
        Apply network settings
      </button>
    </Section>
  );
}

export function LiveControls() {
  const isLive = useClusterStore((s) => s.isLive);
  const cluster = useClusterStore((s) => s.cluster);
  const header = useClusterStore((s) => s.header);
  const liveNet = useClusterStore((s) => s.liveNet);
  const liveSpeed = useClusterStore((s) => s.liveSpeed);
  const commandError = useClusterStore((s) => s.commandError);
  const sendCommand = useClusterStore((s) => s.sendCommand);
  const setLiveSpeed = useClusterStore((s) => s.setLiveSpeed);
  const exportSession = useClusterStore((s) => s.exportSession);

  const nodeIds: NodeID[] = header?.nodes ?? [1, 2, 3, 4, 5];

  // --- propose
  const [key, setKey] = useState("x");
  const [value, setValue] = useState("1");
  const [target, setTarget] = useState(0); // 0 = whoever is leader

  // --- partition: nodes ticked go in group A, everyone else in group B
  const [sideA, setSideA] = useState<NodeID[]>([]);
  const [cutA, setCutA] = useState<NodeID>(nodeIds[0] ?? 1);
  const [cutB, setCutB] = useState<NodeID>(nodeIds[1] ?? 2);
  const toggleA = (id: NodeID) => setSideA((a) => (a.includes(id) ? a.filter((x) => x !== id) : [...a, id]));
  const sideB = nodeIds.filter((id) => !sideA.includes(id));
  const canPartition = sideA.length > 0 && sideB.length > 0;

  const note = cluster.lastPropose;
  const download = () => {
    const text = exportSession();
    if (!text) return;
    const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `raft-session-seed${header?.seed ?? 0}-t${cluster.time}.json`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4" data-testid="live-controls">
      {!isLive && (
        <div className="p-3 rounded-xl bg-slate-800/40 border border-slate-800 text-xs text-slate-400">
          These controls act on a live simulation. Press <strong className="text-sky-300">Start Live Session</strong>{" "}
          (or load a saved session) first.
        </div>
      )}

      {commandError && (
        <div role="alert" className="p-3 rounded-xl bg-rose-950/60 border border-rose-800 text-xs text-rose-200">
          {commandError}
        </div>
      )}

      <Section title="Write data" icon={<Send className="w-3.5 h-3.5 text-sky-400" />}>
        <div className="grid grid-cols-2 gap-2">
          <label className="space-y-1">
            <span className={label}>Key</span>
            <input className={field} value={key} onChange={(e) => setKey(e.target.value)} disabled={!isLive} />
          </label>
          <label className="space-y-1">
            <span className={label}>Value</span>
            <input className={field} value={value} onChange={(e) => setValue(e.target.value)} disabled={!isLive} />
          </label>
        </div>
        <label className="block space-y-1">
          <span className={label}>Send to</span>
          <select className={field} value={target} onChange={(e) => setTarget(Number(e.target.value))} disabled={!isLive}>
            <option value={0}>Current leader</option>
            {nodeIds.map((id) => (
              <option key={id} value={id}>
                Node {id} (rejected unless it leads)
              </option>
            ))}
          </select>
        </label>
        <button
          className={`${btn} w-full bg-sky-600 hover:bg-sky-500 text-white border-sky-500`}
          disabled={!isLive || key.trim() === ""}
          onClick={() => sendCommand({ kind: "propose", key: key.trim(), value, ...(target ? { node: target } : {}) })}
        >
          <Send className="w-3.5 h-3.5" />
          <span>Propose SET {key.trim() || "…"}={value}</span>
        </button>
        {note && (
          <p
            data-testid="propose-note"
            className={`text-[11px] font-mono ${
              note.outcome === "accepted" ? "text-emerald-300" : "text-amber-300"
            }`}
          >
            t={note.t}ms · {note.data} ·{" "}
            {note.outcome === "accepted"
              ? `accepted by node ${note.node}`
              : note.outcome === "rejected"
                ? `rejected: node ${note.node} is not the leader`
                : "no leader right now — nothing was written"}
          </p>
        )}
      </Section>

      {/* keyed on the settings in force, so the drafts restart whenever they change underneath us */}
      <NetworkSection
        key={`${liveNet.drop}/${liveNet.dup}/${liveNet.minDelay}/${liveNet.maxDelay}`}
        live={liveNet}
        enabled={isLive}
        apply={(n) => sendCommand({ kind: "net", ...n })}
      />

      <Section title="Partitions" icon={<Scissors className="w-3.5 h-3.5 text-rose-400" />}>
        <div className="space-y-1">
          <span className={label}>Split the cluster — tick the nodes for side A</span>
          <div className="flex gap-1.5 flex-wrap">
            {nodeIds.map((id) => (
              <button
                key={id}
                disabled={!isLive}
                onClick={() => toggleA(id)}
                aria-pressed={sideA.includes(id)}
                className={`w-8 h-8 rounded-lg text-xs font-bold border transition-all disabled:opacity-40 ${
                  sideA.includes(id)
                    ? "bg-rose-600 border-rose-400 text-white"
                    : "bg-slate-800 border-slate-700 text-slate-400 hover:text-white"
                }`}
              >
                {id}
              </button>
            ))}
          </div>
          <p className="text-[11px] text-slate-500 font-mono">
            A = {sideA.length ? sideA.join(",") : "—"} | B = {sideB.length ? sideB.join(",") : "—"}
          </p>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <button
            className={`${btn} bg-rose-950/70 hover:bg-rose-900 text-rose-300 border-rose-800/60`}
            disabled={!isLive || !canPartition}
            onClick={() => sendCommand({ kind: "partition", groups: [sideA, sideB] })}
          >
            <Scissors className="w-3.5 h-3.5" />
            <span>Partition</span>
          </button>
          <button
            className={`${btn} bg-emerald-600/80 hover:bg-emerald-600 text-white border-emerald-500/60`}
            disabled={!isLive}
            onClick={() => sendCommand({ kind: "heal" })}
          >
            <HeartPulse className="w-3.5 h-3.5" />
            <span>Heal all</span>
          </button>
        </div>

        <div className="pt-2 border-t border-slate-700/50 space-y-2">
          <span className={label}>Cut a single link</span>
          <div className="grid grid-cols-[1fr_auto_1fr_auto] items-center gap-2">
            <select className={field} value={cutA} disabled={!isLive} onChange={(e) => setCutA(Number(e.target.value))}>
              {nodeIds.map((id) => <option key={id} value={id}>Node {id}</option>)}
            </select>
            <span className="text-slate-500 text-xs">⟷</span>
            <select className={field} value={cutB} disabled={!isLive} onChange={(e) => setCutB(Number(e.target.value))}>
              {nodeIds.map((id) => <option key={id} value={id}>Node {id}</option>)}
            </select>
            <button
              className={`${btn} bg-amber-950/70 hover:bg-amber-900 text-amber-300 border-amber-800/60`}
              disabled={!isLive || cutA === cutB}
              onClick={() => sendCommand({ kind: "cut", node: cutA, peer: cutB })}
            >
              Cut
            </button>
          </div>
        </div>
        <p className="text-[11px] text-slate-500 font-mono" data-testid="cut-summary">
          {cluster.cuts.length === 0
            ? "No links are cut."
            : `${cluster.cuts.length} link${cluster.cuts.length === 1 ? "" : "s"} cut: ${cluster.cuts
                .map(([a, b]) => `${a}–${b}`)
                .join(" ")}`}
        </p>
      </Section>

      <Section title="Simulation speed" icon={<SlidersHorizontal className="w-3.5 h-3.5 text-emerald-400" />}>
        <div className="flex items-center rounded-lg bg-slate-800/80 border border-slate-700/60 text-xs overflow-hidden">
          {SPEEDS.map((v) => (
            <button
              key={v}
              disabled={!isLive}
              onClick={() => setLiveSpeed(v)}
              className={`flex-1 px-2 py-1.5 transition-colors disabled:opacity-40 ${
                liveSpeed === v ? "bg-sky-500 text-white font-semibold" : "text-slate-400 hover:text-white"
              }`}
            >
              {v === 0 ? "Pause" : `${v}x`}
            </button>
          ))}
        </div>
        <p className="text-[11px] text-slate-500">
          Speed only changes how fast virtual time passes. It never changes what happens, so a recorded session
          replays identically at any speed.
        </p>
      </Section>

      <Section title="Session" icon={<Download className="w-3.5 h-3.5 text-sky-400" />}>
        <button
          className={`${btn} w-full bg-slate-800 hover:bg-slate-700 text-slate-200 border-slate-600`}
          disabled={!isLive}
          onClick={download}
        >
          <Download className="w-3.5 h-3.5" />
          <span>Download session (.json)</span>
        </button>
        <p className="text-[11px] text-slate-500">
          Seed, settings and every command with the virtual time it took effect. Use “Load” in the header to replay
          it exactly, or <code>go run ./cmd/trace -session file.json</code> for a trace.
        </p>
      </Section>
    </div>
  );
}
