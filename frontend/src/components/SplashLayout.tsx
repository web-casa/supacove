import type { ReactNode } from "react";
import { Brand } from "./Brand";
import { Waveform } from "./ui/Waveform";

const SIGNAL = [{ key: "signal", tone: "signal" as const, kind: "beat" as const }];

/** Full-screen frame for the pre-auth screens: brand, ambient signal, content. */
export function SplashLayout({ children }: { children: ReactNode }) {
  return (
    <main className="splash">
      <div className="splash-bg" aria-hidden>
        <Waveform segments={SIGNAL} />
      </div>
      <Brand />
      {children}
    </main>
  );
}
