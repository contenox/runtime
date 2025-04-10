// components/Label.tsx
import { cn } from "../utils";

type LabelProps = React.LabelHTMLAttributes<HTMLLabelElement>;

export function Label({ className, ...props }: LabelProps) {
  return (
    <label
      className={cn(
        "text-primary-600 dark:text-dark-primary-400 text-sm font-medium",
        className,
      )}
      {...props}
    />
  );
}
