import type { HTMLAttributes, ReactNode } from 'react';

interface CardProps extends HTMLAttributes<HTMLDivElement> {
  title?: string;
  children: ReactNode;
}

export function Card({ title, children, className = '', ...rest }: CardProps) {
  return (
    <section className={`card ${className}`} {...rest}>
      {title && <h3 className="card-title">{title}</h3>}
      {children}
    </section>
  );
}