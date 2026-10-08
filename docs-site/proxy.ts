import { NextResponse, type NextRequest } from "next/server";

// English is served unprefixed from app/(en), Chinese from app/zh. This proxy
// only handles the two cases the file system cannot:
//   /en/…  → 308 to the unprefixed URL (one canonical address per page),
//            remembering English as an explicit choice
//   /      → Chinese browsers are sent to /zh, unless a language was chosen
//            explicitly before (the sc_lang cookie set by the switcher)
const COOKIE = "sc_lang";

/** True when Accept-Language ranks Chinese above English. */
function prefersChinese(header: string | null): boolean {
  if (!header) return false;
  const ranked = header
    .split(",")
    .map((part) => {
      const [tag, q] = part.trim().split(";q=");
      return { tag: tag.toLowerCase(), q: q === undefined ? 1 : Number(q) || 0 };
    })
    // q=0 means "not acceptable": such a tag is never a preference.
    .filter(({ q }) => q > 0)
    .sort((a, b) => b.q - a.q);
  const is = (tag: string, lang: string) => tag === lang || tag.startsWith(`${lang}-`);
  const first = ranked.find(({ tag }) => is(tag, "zh") || is(tag, "en"));
  return first ? is(first.tag, "zh") : false;
}

export function proxy(request: NextRequest) {
  const url = request.nextUrl.clone();
  const { pathname } = url;

  if (pathname === "/en" || pathname.startsWith("/en/")) {
    url.pathname = pathname.slice(3) || "/";
    const response = NextResponse.redirect(url, 308);
    // An /en address is an explicit choice of English: remember it, so the
    // redirect target "/" does not bounce a Chinese browser on to /zh.
    response.cookies.set(COOKIE, "en", { path: "/", maxAge: 31536000, sameSite: "lax" });
    return response;
  }

  let response = NextResponse.next();
  if (!request.cookies.has(COOKIE) && prefersChinese(request.headers.get("accept-language"))) {
    url.pathname = "/zh"; // the query string (campaign parameters) is kept
    response = NextResponse.redirect(url, 307);
  }
  response.headers.set("Vary", "Accept-Language, Cookie");
  return response;
}

export const config = { matcher: ["/", "/en", "/en/:path*"] };
