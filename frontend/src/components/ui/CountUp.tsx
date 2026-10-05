import { useEffect, useRef, useState } from "react";
import { prefersReducedMotion } from "../../lib/motion";

const DURATION_MS = 800;
const round = (n: number) => String(Math.round(n));

interface Props {
  value: number;
  /** Formats every intermediate frame, so the output keeps its final shape. */
  format?: (n: number) => string;
}

/** Rolls from the previously shown number to `value`. */
export function CountUp({ value, format = round }: Props) {
  const [shown, setShown] = useState(() => (prefersReducedMotion() ? value : 0));
  const current = useRef(shown);

  useEffect(() => {
    const from = current.current;
    if (from === value) return;
    const duration = prefersReducedMotion() ? 0 : DURATION_MS;
    const start = performance.now();
    let raf = requestAnimationFrame(function tick(now) {
      const p = duration === 0 ? 1 : Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - p, 4);
      current.current = p === 1 ? value : from + (value - from) * eased;
      setShown(current.current);
      if (p < 1) raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [value]);

  return <>{format(shown)}</>;
}
