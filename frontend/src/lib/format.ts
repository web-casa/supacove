// Display formatting shared by every panel. All timestamps from the API are
// unix seconds. Language-sensitive helpers read the active language via
// i18n/getLang() so plain (non-React) call sites stay localized too; React
// components re-render through useI18n() when it changes.
import { getLang, translate } from "../i18n/core";

export function relativeTime(
  unixSecs: number,
  nowMs: number = Date.now(),
): string {
  const s = Math.max(0, Math.round(nowMs / 1000 - unixSecs));
  if (getLang() === "zh-CN") {
    if (s < 45) return "刚刚";
    if (s < 3600) return `${Math.max(1, Math.round(s / 60))} 分钟前`;
    if (s < 48 * 3600) return `${Math.round(s / 3600)} 小时前`;
    if (s < 60 * 86400) return `${Math.round(s / 86400)} 天前`;
    return `${Math.round(s / (30 * 86400))} 个月前`;
  }
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m ago`;
  if (s < 48 * 3600) return `${Math.round(s / 3600)}h ago`;
  if (s < 60 * 86400) return `${Math.round(s / 86400)}d ago`;
  return `${Math.round(s / (30 * 86400))}mo ago`;
}

export function absoluteTime(unixSecs: number): string {
  return new Date(unixSecs * 1000).toLocaleString(
    getLang() === "zh-CN" ? "zh-CN" : "en",
    {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      timeZoneName: "short",
    },
  );
}

export function humanBytes(b?: number | null): string {
  if (b == null || b <= 0) return "—";
  if (b < 1024) return `${b} B`;
  if (b < 1024 ** 2) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 ** 3) return `${(b / 1024 ** 2).toFixed(1)} MB`;
  return `${(b / 1024 ** 3).toFixed(2)} GB`;
}

export function pct(v?: number | null): string {
  if (v == null) return "—";
  return `${v.toFixed(1)}%`;
}

export function duration(secs?: number | null): string {
  if (secs == null) return "—";
  if (secs < 60) return `${secs.toFixed(1)}s`;
  const m = Math.floor(secs / 60);
  const s = Math.round(secs - m * 60);
  return `${m}m ${String(s).padStart(2, "0")}s`;
}

export function errorMessage(e: unknown): string {
  return e instanceof Error && e.message
    ? e.message
    : translate(getLang(), "fmt.requestFailed");
}
