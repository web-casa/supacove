import { source } from "@/lib/source";
import { createFromSource } from "fumadocs-core/search/server";

const search = createFromSource(source);

/**
 * Static export (Cloudflare Pages): the ENTIRE index is baked into
 * out/api/search at build time. The dialog's `type: "static"` client
 * (orama-static, wired in lib/providers.tsx) fetches this file once and
 * runs every query client-side — no server answers ?query= anymore.
 * The noindex header lives in public/_headers because a static file
 * cannot carry route-handler headers.
 */
export const dynamic = "force-static";

export function GET() {
  return search.staticGET();
}
