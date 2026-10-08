"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Cycles through `words` in place, one every `interval` ms. The slot's width
 * follows the current word (measured from hidden copies) so the rest of the
 * headline glides instead of jumping. Decorative: the parent heading carries
 * an aria-label with the full list.
 */
export function Rotator({ words, interval = 2000 }: { words: readonly string[]; interval?: number }) {
  const [index, setIndex] = useState(0);
  const [width, setWidth] = useState<number>();
  const sizer = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    // People who asked for reduced motion get the first word, standing still.
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    let id: ReturnType<typeof setInterval> | undefined;
    const sync = () => {
      clearInterval(id);
      if (reduced.matches) setIndex(0);
      else id = setInterval(() => setIndex((i) => (i + 1) % words.length), interval);
    };
    sync();
    reduced.addEventListener("change", sync);
    return () => {
      clearInterval(id);
      reduced.removeEventListener("change", sync);
    };
  }, [words.length, interval]);

  useEffect(() => {
    const measure = () => setWidth(sizer.current?.children[index]?.getBoundingClientRect().width);
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [index]);

  return (
    <span className="hv-rot" style={{ width }} aria-hidden>
      <span key={index} className="hv-rot-word">
        {words[index]}
      </span>
      <span ref={sizer} className="hv-rot-sizer">
        {words.map((word) => (
          <span key={word}>{word}</span>
        ))}
      </span>
    </span>
  );
}
