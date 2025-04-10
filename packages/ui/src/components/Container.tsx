import { H1 } from "./Typography";
import { cn } from "../utils";

interface ContainerProps {
  title?: string;
  className?: string;
  children: React.ReactNode;
}

export function Container({ title, className, children }: ContainerProps) {
  return (
    <div className={cn(`container mx-auto space-y-6 p-6`, className)}>
      {title && <H1>{title}</H1>}
      <div className="bg-inherit p-4">{children}</div>
    </div>
  );
}
