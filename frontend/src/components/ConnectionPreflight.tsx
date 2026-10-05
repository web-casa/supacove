import { CircleAlert, CircleCheck, Info, TriangleAlert, Wand2 } from "lucide-react";
import type { Analysis, Finding, HostPatch } from "../lib/connection";
import { Button } from "./ui/Button";

const LOOK: Record<Finding["level"], { tone: string; icon: typeof Info }> = {
  ok: { tone: "ok", icon: CircleCheck },
  info: { tone: "neutral", icon: Info },
  warn: { tone: "warn", icon: TriangleAlert },
  error: { tone: "danger", icon: CircleAlert },
};

interface Props {
  analysis: Analysis;
  onFix: (patch: HostPatch) => void;
}

/** What the pre-flight found, and exactly what will be sent (password masked). */
export function ConnectionPreflight({ analysis, onFix }: Props) {
  return (
    <div className="preflight" role="status" aria-label="Connection pre-flight">
      {analysis.redacted && (
        <p className="preflight-target">
          <span className="muted">Will register</span>
          <code>{analysis.redacted}</code>
        </p>
      )}
      <ul>
        {analysis.findings.map((f) => {
          const { tone, icon: Icon } = LOOK[f.level];
          return (
            <li className={`finding tone-${tone}`} key={f.text}>
              <Icon size={14} aria-hidden />
              <span>{f.text}</span>
              {f.fix && (
                <Button size="sm" icon={Wand2} onClick={() => onFix(f.fix!.patch)}>
                  {f.fix.label}
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
