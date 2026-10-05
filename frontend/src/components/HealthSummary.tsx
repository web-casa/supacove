import { useQuery } from "@tanstack/react-query";
import { PauseCircle, RefreshCw, ShieldAlert, ShieldCheck, ShieldOff, type LucideIcon } from "lucide-react";
import { errorMessage } from "../lib/format";
import { spotlight } from "../lib/motion";
import { overviewQuery, statsQuery } from "../lib/queries";
import { bySeverity, summarize, type HealthSummary as Summary, type Tone } from "../lib/status";
import { VitalsStrip } from "./VitalsStrip";
import { Beacon } from "./ui/Beacon";
import { Button } from "./ui/Button";
import { CountUp } from "./ui/CountUp";
import { InlineMessage } from "./ui/InlineMessage";
import { RelativeTime } from "./ui/RelativeTime";
import { SkeletonText } from "./ui/Skeleton";

function headline(s: Summary): string {
  if (s.total === 0) return "No databases registered yet";
  const noun = s.total === 1 ? "database" : "databases";
  if (s.needAttention === 0) return s.total === 1 ? "Your database is protected" : `All ${s.total} databases are protected`;
  return `${s.needAttention} of ${s.total} ${noun} ${s.needAttention === 1 ? "needs" : "need"} attention`;
}

function detail(s: Summary): string {
  if (s.total === 0) return "Register a database below to start taking encrypted backups.";
  const parts = [
    s.expired > 0 && `${s.expired} expired`,
    s.never > 0 && `${s.never} never backed up`,
    s.verifyFailed > 0 && `${s.verifyFailed} failed restore verification`,
    s.lastJobFailed > 0 && `${s.lastJobFailed} with a failed last job`,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "Every database has a fresh successful backup.";
}

interface TileProps {
  tone: Tone;
  icon: LucideIcon;
  label: string;
  value: number | undefined;
}

function Tile({ tone, icon: Icon, label, value }: TileProps) {
  return (
    <div className={`tile tone-${tone}${value ? " tile-active" : ""}`}>
      <span className="tile-label">
        <Icon size={13} aria-hidden />
        {label}
      </span>
      <span className="tile-value num">{value === undefined ? <SkeletonText short /> : <CountUp value={value} />}</span>
    </div>
  );
}

/** First-screen answer to "is everything OK?". */
export function HealthSummary() {
  const overview = useQuery(overviewQuery);
  const stats = useQuery(statsQuery);
  const s = overview.data ? summarize(overview.data.databases) : undefined;
  const tone = s?.tone ?? "neutral";
  const lastSuccessAt = stats.data?.lastSuccessAt || s?.lastSuccessAt || null;

  return (
    <section className={`health spotlight tone-${tone}`} aria-label="Protection health" onPointerMove={spotlight}>
      <div className="health-head">
        <div className="health-text">
          <p className="eyebrow">
            <Beacon tone={tone} live={tone !== "neutral"} />
            Protection status
          </p>
          <h1>{s ? headline(s) : <SkeletonText />}</h1>
          <p className="muted">{s ? detail(s) : overview.isError ? "Protection state is unknown." : "Loading protection state…"}</p>
        </div>
        <div className="health-last tip-end">
          <span className="muted">Last successful backup</span>
          <strong>{lastSuccessAt ? <RelativeTime at={lastSuccessAt} /> : s || stats.data ? "never" : <SkeletonText short />}</strong>
        </div>
        <Button
          variant="ghost"
          size="sm"
          icon={RefreshCw}
          tip="Refresh now (auto-refreshes every 30s)"
          className="tip-end"
          loading={overview.isFetching && !overview.isPending}
          onClick={() => {
            overview.refetch();
            stats.refetch();
          }}
        />
      </div>

      {overview.isError && <InlineMessage>Overview unavailable: {errorMessage(overview.error)}</InlineMessage>}

      {overview.data && s && s.total > 0 && <VitalsStrip dbs={bySeverity(overview.data.databases)} />}

      <div className="tiles stagger">
        <Tile tone="ok" icon={ShieldCheck} label="Protected" value={s?.fresh} />
        <Tile tone="danger" icon={ShieldAlert} label="Expired" value={s?.expired} />
        <Tile tone="warn" icon={ShieldOff} label="Never backed up" value={s?.never} />
        <Tile tone="neutral" icon={PauseCircle} label="Paused" value={s?.paused} />
      </div>
    </section>
  );
}
