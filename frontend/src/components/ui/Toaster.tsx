import { useCallback, useRef, useState, type ReactNode } from "react";
import { CircleAlert, CircleCheck } from "lucide-react";
import { ToastContext, type PushToast } from "../../lib/toast";
import type { Tone } from "../../lib/status";

interface Toast {
  id: number;
  tone: Tone;
  text: string;
}

const LIFETIME_MS = 4500;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(1);

  const push = useCallback<PushToast>((tone, text) => {
    const id = nextId.current++;
    setToasts((cur) => [...cur.slice(-2), { id, tone, text }]);
    window.setTimeout(() => setToasts((cur) => cur.filter((t) => t.id !== id)), LIFETIME_MS);
  }, []);

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div className={`toast tone-${t.tone}`} key={t.id}>
            {t.tone === "danger" ? <CircleAlert size={15} aria-hidden /> : <CircleCheck size={15} aria-hidden />}
            {t.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
