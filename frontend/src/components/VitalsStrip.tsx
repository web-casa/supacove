import type { OverviewEntry } from "../api/client";
import { useI18n } from "../i18n";
import { protectionMeta, rowTone, waveKind } from "../lib/status";
import { Waveform } from "./ui/Waveform";

const MAX_LABELLED = 8;

/** One trace per database: protected ones beat, expired ones flatline. */
export function VitalsStrip({ dbs }: { dbs: OverviewEntry[] }) {
  const { t } = useI18n();
  const summary = dbs.map((d) => `${d.name}: ${t(protectionMeta(d.state).label)}`).join(", ");
  return (
    <div className="vitals" role="img" aria-label={t("vitals.aria", { summary })}>
      <div className="vitals-screen">
        <Waveform segments={dbs.map((d) => ({ key: d.databaseId, tone: rowTone(d), kind: waveKind(d) }))} />
        <div className="vitals-cells">
          {dbs.map((d) => (
            <span
              key={d.databaseId}
              className="vitals-cell"
              data-tip={`${d.name} — ${t(protectionMeta(d.state).label)}${d.schedulePaused ? `, ${t("vitals.paused")}` : ""}`}
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
