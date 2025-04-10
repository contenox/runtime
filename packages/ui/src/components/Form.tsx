// components/Form.tsx
import { cn } from "../utils";
import { Panel } from "./Panel";
import { H2 } from "./Typography";

type FormProps = {
  title?: string;
  onSubmit: (e: React.FormEvent) => void;
  error?: string;
  onError?: (error: string) => void;
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
};

export function Form({
  title,
  onSubmit,
  error,
  onError,
  actions,
  children,
  className,
}: FormProps) {
  return (
    <Panel variant="bordered" className={cn("p-6", className)}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          try {
            onSubmit(e);
          } catch (err) {
            onError?.(err instanceof Error ? err.message : String(err));
          }
        }}
        className="space-y-6"
      >
        {title && (
          <H2 className="text-text dark:text-dark-text text-2xl font-semibold">
            {title}
          </H2>
        )}
        <div className="space-y-4">{children}</div>

        {error && <Panel variant="error">{error}</Panel>}

        <div className="flex gap-4">{actions}</div>
      </form>
    </Panel>
  );
}
