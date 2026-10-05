// Per-platform guidance for the registration form: where the connection
// string lives and which variant pg_dump can use. Copy lives in i18n/en.ts
// and i18n/zh-CN.ts under `providers.<id>.*`; `label` is a brand name and
// stays untranslated.
import { Leaf, Server, TrainFront, Zap, type LucideIcon } from "lucide-react";
import type { Provider, SslMode } from "./connection";

export interface ProviderInfo {
  id: Provider;
  /** Brand name (untranslated), unless labelKey is set. */
  label?: string;
  /** i18n key for non-brand category names. */
  labelKey?: string;
  /** i18n key for the one-line summary under the brand name. */
  taglineKey: string;
  icon: LucideIcon;
  /** Shown inside the connection-string input. */
  example: string;
  defaultSsl: SslMode;
  /** Self-hosted servers rarely hand out a ready-made URI. */
  defaultMode: "uri" | "fields";
  /** i18n keys for the numbered instructions. */
  stepKeys: string[];
}

export const PROVIDERS: ProviderInfo[] = [
  {
    id: "supabase",
    label: "Supabase",
    taglineKey: "providers.supabase.tagline",
    icon: Zap,
    example: "postgresql://postgres.<ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/postgres",
    defaultSsl: "require",
    defaultMode: "uri",
    stepKeys: ["providers.supabase.s1", "providers.supabase.s2", "providers.supabase.s3", "providers.supabase.s4"],
  },
  {
    id: "neon",
    label: "Neon",
    taglineKey: "providers.neon.tagline",
    icon: Leaf,
    example: "postgresql://<user>:<password>@ep-<name>.<region>.aws.neon.tech/<db>?sslmode=require",
    defaultSsl: "require",
    defaultMode: "uri",
    stepKeys: ["providers.neon.s1", "providers.neon.s2", "providers.neon.s3", "providers.neon.s4"],
  },
  {
    id: "railway",
    label: "Railway",
    taglineKey: "providers.railway.tagline",
    icon: TrainFront,
    example: "postgresql://postgres:<password>@<name>.proxy.rlwy.net:<port>/railway",
    defaultSsl: "require",
    defaultMode: "uri",
    stepKeys: ["providers.railway.s1", "providers.railway.s2", "providers.railway.s3", "providers.railway.s4"],
  },
  {
    id: "generic",
    labelKey: "providers.generic.label",
    taglineKey: "providers.generic.tagline",
    icon: Server,
    example: "postgresql://backup:<password>@db.internal:5432/app?sslmode=verify-full",
    defaultSsl: "require",
    defaultMode: "fields",
    stepKeys: ["providers.generic.s1", "providers.generic.s2", "providers.generic.s3", "providers.generic.s4"],
  },
];

export const providerInfo = (id: Provider): ProviderInfo => PROVIDERS.find((p) => p.id === id) ?? PROVIDERS[3];
