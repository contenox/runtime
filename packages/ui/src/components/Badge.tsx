// TODO add color entries: components/Badge.tsx
import { cn } from "../utils";

type BadgeProps = React.HTMLAttributes<HTMLSpanElement> & {
  variant?: "default" | "success" | "error" | "warning";
  size?: "sm" | "md";
};

export function Badge({
  className,
  variant = "default",
  size = "md",
  ...props
}: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full font-medium",
        "border-secondary-200 dark:border-dark-secondary-300 border",
        {
          "px-2.5 py-0.5 text-xs": size === "sm",
          "px-3 py-1 text-sm": size === "md",
          "bg-primary-100 text-primary-800 dark:bg-dark-primary-900 dark:text-dark-primary-300":
            variant === "default",
          // Ensure your theme defines these dark success colors:
          "bg-green-100 text-green-800 dark:bg-dark-success-900 dark:text-dark-success-300":
            variant === "success",
          "bg-red-100 text-red-800 dark:bg-dark-error-900 dark:text-dark-error-300":
            variant === "error",
          "bg-yellow-100 text-yellow-800 dark:bg-dark-warning-900 dark:text-dark-warning-300":
            variant === "warning",
        },
        className,
      )}
      {...props}
    />
  );
}
