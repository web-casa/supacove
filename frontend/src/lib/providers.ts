// Per-platform guidance for the registration form: where the connection
// string lives and which variant pg_dump can use.
import { Leaf, Server, TrainFront, Zap, type LucideIcon } from "lucide-react";
import type { Provider, SslMode } from "./connection";

export interface ProviderInfo {
  id: Provider;
  label: string;
  tagline: string;
  icon: LucideIcon;
  /** Shown inside the connection-string input. */
  example: string;
  defaultSsl: SslMode;
  /** Self-hosted servers rarely hand out a ready-made URI. */
  defaultMode: "uri" | "fields";
  steps: string[];
}

export const PROVIDERS: ProviderInfo[] = [
  {
    id: "supabase",
    label: "Supabase",
    tagline: "Session pooler or direct",
    icon: Zap,
    example: "postgresql://postgres.<ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/postgres",
    defaultSsl: "require",
    defaultMode: "uri",
    steps: [
      "In the project dashboard open Connect, then the Connection string tab (type: URI).",
      "Copy Session pooler (port 5432). It works over IPv4. Direct connection also works, but it is IPv6-only unless the project has the IPv4 add-on.",
      "Do not use Transaction pooler (port 6543) — pg_dump cannot run through it.",
      "Replace [YOUR-PASSWORD] with the database password.",
    ],
  },
  {
    id: "neon",
    label: "Neon",
    tagline: "Direct endpoint, pooling off",
    icon: Leaf,
    example: "postgresql://<user>:<password>@ep-<name>.<region>.aws.neon.tech/<db>?sslmode=require",
    defaultSsl: "require",
    defaultMode: "uri",
    steps: [
      "In the Neon console open the project and click Connect.",
      "Switch Connection pooling off, so the host has no “-pooler” in it.",
      "Copy the string as-is. The channel_binding parameter Neon adds is removed here because supabackup does not accept it.",
      "An idle compute takes a few seconds to wake, so the first test can be slow.",
    ],
  },
  {
    id: "railway",
    label: "Railway",
    tagline: "Public TCP proxy URL",
    icon: TrainFront,
    example: "postgresql://postgres:<password>@<name>.proxy.rlwy.net:<port>/railway",
    defaultSsl: "require",
    defaultMode: "uri",
    steps: [
      "Open the Postgres service, then the Variables tab.",
      "Copy DATABASE_PUBLIC_URL (host ends in .proxy.rlwy.net).",
      "Do not use DATABASE_URL: its *.railway.internal host only resolves inside Railway.",
      "Railway’s string sets no TLS mode, so one is added from the selector below.",
    ],
  },
  {
    id: "generic",
    label: "Self-hosted / other",
    tagline: "Any PostgreSQL server",
    icon: Server,
    example: "postgresql://backup:<password>@db.internal:5432/app?sslmode=verify-full",
    defaultSsl: "require",
    defaultMode: "fields",
    steps: [
      "The host must be reachable from the supabackup container. localhost means the container itself; for the Docker host use host.docker.internal or its LAN address.",
      "Use a role that can read everything pg_dump exports: the database owner, or a member of pg_read_all_data (PostgreSQL 14+).",
      "TLS: verify-full when the server has a CA-signed certificate, require to encrypt without verifying, disable only on a private network you trust.",
      "Check pg_hba.conf allows this server’s address for that role and database.",
    ],
  },
];

export const providerInfo = (id: Provider): ProviderInfo => PROVIDERS.find((p) => p.id === id) ?? PROVIDERS[3];
