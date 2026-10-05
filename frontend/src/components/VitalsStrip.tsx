import type { OverviewEntry } from "../api/client";
import { protectionMeta, rowTone, waveKind } from "../lib/status";
import { Waveform } from "./ui/Waveform";

const MAX_LABELLED = 8;

/** One trace per database: protected ones beat, expired ones flatline. */
export function VitalsStrip({ dbs }: { dbs: OverviewEntry[] }) {
  const summary = dbs.map((d) => `${d.name}: ${protectionMeta(d.state).label}`).join(", ");
  return (
    <div className="vitals" role="img" aria-label={`Database vitals — ${summary}`}>
      <div className="vitals-screen">
        <Waveform segments={dbs.map((d) => ({ key: d.databaseId, tone: rowTone(d), kind: waveKind(d) }))} />
        <div className="vitals-cells">
          {dbs.map((d) => (
            <span
              key={d.databaseId}
              className="vitals-cell"
              data-tip={`${d.name} — ${protectionMeta(d.state).label}${d.schedulePaused ? ", paused" : ""}`}
            />
          ))}
        </div>
      </div>
      {dbs.length <= MAX_LABELLED && (
        <div className="vitals-names" aria-hidden>
          {dbs.map((d) => (
            <span key={d.databaseId} className={`truncate tone-${rowTone(d)}`}>
              {d.name}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
