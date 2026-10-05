// Single source of truth for how API states map to label + colour + icon.
import {
  Ban,
  CircleCheck,
  CircleDashed,
  CircleHelp,
  CircleX,
  Clock,
  LoaderCircle,
  MailCheck,
  MailX,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  type LucideIcon,
} from "lucide-react";
import type { NotificationList, OverviewEntry, Task } from "../api/client";

export type Tone = "ok" | "danger" | "warn" | "neutral" | "info";

/** Waveform drawn for a database on the vitals strip. */
export type WaveKind = "beat" | "flat" | "dash" | "bump";

export interface StatusMeta {
  label: string;
  tone: Tone;
  icon: LucideIcon;
  spin?: boolean;
}

export function protectionMeta(state: OverviewEntry["state"]): StatusMeta {
  switch (state) {
    case "fresh":
      return { label: "protected", tone: "ok", icon: ShieldCheck };
    case "expired":
      return { label: "EXPIRED", tone: "danger", icon: ShieldAlert };
    case "never":
      return { label: "never backed up", tone: "warn", icon: ShieldOff };
    default:
      return { label: state, tone: "neutral", icon: CircleHelp };
  }
}

export function verifyMeta(v?: string): StatusMeta | null {
  switch (v) {
    case "verified":
      return { label: "restore-verified", tone: "ok", icon: CircleCheck };
    case "failed":
      return { label: "verify FAILED", tone: "danger", icon: CircleX };
    case "unsupported":
      return { label: "verify unsupported", tone: "warn", icon: CircleHelp };
    case "pending":
      return { label: "verify pending…", tone: "neutral", icon: Clock };
    case "running":
      return { label: "verify running…", tone: "info", icon: LoaderCircle, spin: true };
    case "skipped":
      return { label: "not verified", tone: "neutral", icon: CircleDashed };
    default:
      return null;
  }
}

export function taskStatusMeta(status: Task["status"]): StatusMeta {
  switch (status) {
    case "succeeded":
      return { label: "succeeded", tone: "ok", icon: CircleCheck };
    case "failed":
      return { label: "failed", tone: "danger", icon: CircleX };
    case "running":
      return { label: "running", tone: "info", icon: LoaderCircle, spin: true };
    case "pending":
      return { label: "pending", tone: "neutral", icon: Clock };
    case "interrupted":
      return { label: "interrupted", tone: "warn", icon: CircleHelp };
    case "canceled":
      return { label: "canceled", tone: "neutral", icon: Ban };
    default:
      return { label: status, tone: "neutral", icon: CircleHelp };
  }
}

type DeliveryState = NotificationList["notifications"][number]["state"];

export function deliveryMeta(state: DeliveryState): StatusMeta {
  switch (state) {
    case "delivered":
      return { label: "delivered", tone: "ok", icon: MailCheck };
    case "dead":
      return { label: "dead", tone: "danger", icon: MailX };
    case "delivering":
      return { label: "delivering", tone: "info", icon: LoaderCircle, spin: true };
    default:
      return { label: state, tone: "neutral", icon: Clock };
  }
}

export interface HealthSummary {
  total: number;
  fresh: number;
  expired: number;
  never: number;
  paused: number;
  verifyFailed: number;
  lastJobFailed: number;
  needAttention: number;
  tone: Tone;
  lastSuccessAt: number | null;
}

const needsAttention = (d: OverviewEntry) =>
  d.state !== "fresh" || d.lastSuccessVerifyStatus === "failed" || d.lastJobStatus === "failed";

export function summarize(dbs: OverviewEntry[]): HealthSummary {
  const count = (f: (d: OverviewEntry) => boolean) => dbs.filter(f).length;
  const expired = count((d) => d.state === "expired");
  const never = count((d) => d.state === "never");
  const verifyFailed = count((d) => d.lastSuccessVerifyStatus === "failed");
  const lastJobFailed = count((d) => d.lastJobStatus === "failed");
  const needAttention = count(needsAttention);
  const tone: Tone =
    dbs.length === 0
      ? "neutral"
      : expired > 0 || verifyFailed > 0
        ? "danger"
        : needAttention > 0
          ? "warn"
          : "ok";
  const successes = dbs.map((d) => d.lastSuccessAt ?? 0).filter((t) => t > 0);
  return {
    total: dbs.length,
    fresh: count((d) => d.state === "fresh"),
    expired,
    never,
    paused: count((d) => d.schedulePaused === true),
    verifyFailed,
    lastJobFailed,
    needAttention,
    tone,
    lastSuccessAt: successes.length > 0 ? Math.max(...successes) : null,
  };
}

// Problems first so the eye lands on them; stable within a group.
const severity: Record<string, number> = { expired: 0, never: 1, fresh: 2 };

export function bySeverity(dbs: OverviewEntry[]): OverviewEntry[] {
  return [...dbs].sort((a, b) => {
    const sa = (severity[a.state] ?? 3) - (needsAttention(a) ? 0.5 : 0);
    const sb = (severity[b.state] ?? 3) - (needsAttention(b) ? 0.5 : 0);
    return sa - sb;
  });
}

/** Unix seconds of the last success; falls back to the server-computed age. */
export function lastSuccessUnix(d: OverviewEntry): number | null {
  if (d.lastSuccessAt != null) return d.lastSuccessAt;
  if (d.lastSuccessAgeHours != null) return Date.now() / 1000 - d.lastSuccessAgeHours * 3600;
  return null;
}

/** Grey = paused, unless something is actually wrong. */
export function rowTone(d: OverviewEntry): Tone {
  return d.schedulePaused && d.state === "fresh" ? "neutral" : protectionMeta(d.state).tone;
}

/** Protected databases beat; expired ones flatline; never-backed-up have no signal yet. */
export function waveKind(d: OverviewEntry): WaveKind {
  if (d.state === "expired") return "flat";
  if (d.state === "never") return "dash";
  return d.schedulePaused ? "bump" : "beat";
}
