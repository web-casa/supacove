import { useEffect, useState } from "react";
import { flushSync } from "react-dom";
import { prefersReducedMotion } from "./motion";

// The console has four views; the active one lives in the URL hash so a
// reload or a shared link lands on the same view without a router.
export const VIEWS = [
  "overview",
  "backups",
  "storage",
  "notifications",
] as const;
export type View = (typeof VIEWS)[number];

function read(): View {
  const h = window.location.hash.replace(/^#\/?/, "");
  return (VIEWS as readonly string[]).includes(h) ? (h as View) : "overview";
}

export function useHashView(): [View, (v: View) => void] {
  const [view, setView] = useState<View>(read);
  useEffect(() => {
    const onHash = () => {
      // Cross-fade between views where the browser supports view transitions.
      if (
        typeof document.startViewTransition === "function" &&
        !prefersReducedMotion()
      ) {
        document.startViewTransition(() => flushSync(() => setView(read())));
      } else {
        setView(read());
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  return [
    view,
    (v) => {
      window.location.hash = `/${v}`;
    },
  ];
}
