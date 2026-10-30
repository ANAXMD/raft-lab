import { useEffect, useState } from "react";
import { getSim } from "./store";

/** True once sim.wasm has loaded and published the RaftSim global (it loads asynchronously). */
export function useSimReady(): boolean {
  const [ready, setReady] = useState(() => getSim() !== undefined);
  useEffect(() => {
    if (ready) return;
    const id = setInterval(() => {
      if (getSim()) {
        setReady(true);
        clearInterval(id);
      }
    }, 150);
    return () => clearInterval(id);
  }, [ready]);
  return ready;
}
