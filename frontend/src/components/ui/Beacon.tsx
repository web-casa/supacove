import type { Tone } from "../../lib/status";

/** Status dot; `live` adds the radiating ping. */
export function Beacon({ tone, live }: { tone: Tone | "signal"; live?: boolean }) {
  return <span className={`beacon tone-${tone}${live ? " beacon-live" : ""}`} aria-hidden />;
}
