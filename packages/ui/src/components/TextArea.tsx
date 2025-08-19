import { forwardRef } from "react";
import type { ClassValue } from "clsx";
import { clsx } from "clsx";
import { twMerge } from "tailwind-merge";
import { cn } from "../utils";

type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement> & {
  error?: boolean;
};

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, error = false, ...props }, ref) => {
    return (
      <div className="relative w-full">
        <textarea
          ref={ref}
          className={cn(
            // Base styles
            "bg-surface-50 text-text w-full rounded-lg border px-4 py-2.5 min-h-[120px] resize-y transition-colors",
            "focus:ring-primary-500 focus:ring-2 focus:ring-offset-2 focus:outline-none",
            // Dark mode styles
            "dark:bg-dark-surface-200 dark:text-dark-text dark:focus:ring-dark-primary-500 dark:focus:ring-offset-dark-surface-100",
            // Error and default border states
            error
              ? "border-error-300 focus:border-error-500 focus:ring-error-500 dark:border-dark-error-400 dark:focus:border-dark-error-500 dark:focus:ring-dark-error-500"
              : "border-surface-300 dark:border-dark-surface-600 focus:border-primary-500",
            className,
          )}
          {...props}
        />
      </div>
    );
  },
);

Textarea.displayName = "Textarea";
