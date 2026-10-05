import type { Tone, WaveKind } from "../../lib/status";

export interface WaveSegment {
  key: string | number;
  tone: Tone | "signal";
  kind: WaveKind;
}

/** Monitor-style trace: a dim base line with a bright sweep running over it. */
export function Waveform({ segments }: { segments: WaveSegment[] }) {
  return (
    <div className="waveform" aria-hidden>
      {(["base", "sweep"] as const).map((layer) => (
        <div className={`wave-layer wave-${layer}`} key={layer}>
          {segments.map((s) => (
            <span className={`wave-seg wave-${s.kind} tone-${s.tone}`} key={s.key} />
          ))}
        </div>
      ))}
    </div>
  );
}
