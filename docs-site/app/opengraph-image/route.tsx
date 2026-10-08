import { ImageResponse } from "next/og";

// One branded share image for every page and both locales, at /opengraph-image.
// Latin text only: the bundled font has no CJK glyphs.
// A route handler rather than the opengraph-image file convention: at the app
// root there is no layout to give that convention a metadataBase (each locale
// has its own root layout), so pages name this URL themselves via OG_IMAGE.
export const dynamic = "force-static";

const size = { width: 1200, height: 630 };

export function GET() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: 72,
          background: "#101317",
          color: "#e6e9ee",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 20, fontSize: 40, fontWeight: 600 }}>
          <svg width="56" height="56" viewBox="0 0 24 24" fill="none" stroke="#2fe6c8" strokeWidth="1.8" strokeLinecap="round">
            <ellipse cx="12" cy="6.5" rx="7.5" ry="3" />
            <path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11" />
            <path d="M4.5 12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
          </svg>
          SupaCove
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 24 }}>
          <div style={{ fontSize: 76, fontWeight: 700, lineHeight: 1.08, letterSpacing: -3 }}>
            Encrypted PostgreSQL backups, in storage you own.
          </div>
          <div style={{ fontSize: 30, color: "#2fe6c8" }}>Supabase · Neon · Railway · self-hosted — supacove.com</div>
        </div>
      </div>
    ),
    size,
  );
}
