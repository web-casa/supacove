import type { MetadataRoute } from "next";
import { SITE_URL } from "@/lib/site";

// One group for everyone, search and AI crawlers alike. /api/search stays
// crawlable on purpose: it answers with X-Robots-Tag: noindex (set in
// public/_headers for the static build), which a crawler can only see if it
// is allowed to fetch the URL.
/** Static export: generated once at build time. */
export const dynamic = "force-static";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [{ userAgent: "*", allow: "/" }],
    sitemap: `${SITE_URL}/sitemap.xml`,
  };
}
