import { forwardRef } from "react";
import { cn } from "../utils";

type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement> & {
  startIcon?: React.ReactNode;
  endIcon?: React.ReactNode;
  error?: boolean;
};

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, startIcon, endIcon, error = false, ...props }, ref) => {
    return (
      <div className="relative w-full">
        {startIcon && (
          <div className="absolute top-3 left-3 text-secondary-400 dark:text-dark-secondary-400">
            {startIcon}
          </div>
        )}
        <textarea
          ref={ref}
          className={cn(
            "bg-surface-50 text-text w-full rounded-lg border px-4 py-2.5 min-h-[120px] resize-y",
            "focus:ring-primary-500 focus:ring-2 focus:ring-offset-2",
            "dark:border-dark-secondary-300 dark:bg-dark-surface-50 dark:text-dark-text dark:focus:ring-dark-primary-500",
            startIcon && "pl-10",
            endIcon && "pr-10",
            error
              ? "border-error-300 focus:ring-error-500 dark:border-dark-error-300 dark:focus:ring-dark-error-500"
              : "border-secondary-300 dark:border-dark-secondary-300 focus:border-transparent",
            className,
          )}
          {...props}
        />
        {endIcon && (
          <div className="absolute top-3 right-3 text-secondary-400 dark:text-dark-secondary-400">
            {endIcon}
          </div>
        )}
      </div>
    );
  },
);

Textarea.displayName = "Textarea";
