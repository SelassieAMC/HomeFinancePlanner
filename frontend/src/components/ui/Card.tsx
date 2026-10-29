import type { HTMLAttributes, ReactNode } from 'react';
import { HelpHint } from './Feedback';

interface CardProps extends HTMLAttributes<HTMLDivElement> {
  title?: string;
  /** Explainer text behind the question-circle trigger beside the title —
   *  a click-opened floating tooltip, not printed text. Statuses, warnings
   *  and error text stay visible in the body; this is context. */
  help?: ReactNode;
  children: ReactNode;
}

export function Card({ title, help, children, className = '', ...rest }: CardProps) {
  return (
    <section className={`card ${className}`} {...rest}>
      {title && (
        <h3 className="card-title">
          {title}
          {help != null && <HelpHint note={help} />}
        </h3>
      )}
      {children}
    </section>
  );
}