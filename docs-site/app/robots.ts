import type { MetadataRoute } from "next";
import { SITE_URL } from "@/lib/site";

// One group for everyone, search and AI crawlers alike. /api/search stays
// crawlable on purpose: it answers with X-Robots-Tag: noindex, which a
// crawler can only see if it is allowed to fetch the URL.
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [{ userAgent: "*", allow: "/" }],
    sitemap: `${SITE_URL}/sitemap.xml`,
  };
}
