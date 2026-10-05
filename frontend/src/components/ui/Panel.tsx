import type { ReactNode } from "react";

interface Props {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}

export function Panel({ title, description, actions, children }: Props) {
  return (
    <section className="panel">
      <header className="panel-head">
        <div className="panel-title">
          <h2>{title}</h2>
          {description && <p>{description}</p>}
        </div>
        {actions && <div className="panel-actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}
