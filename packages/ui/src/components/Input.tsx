// components/Input.tsx
import { forwardRef, useState } from "react";
import { cn } from "../utils";

type InputProps = React.InputHTMLAttributes<HTMLInputElement> & {
  startIcon?: React.ReactNode;
  endIcon?: React.ReactNode;
  error?: boolean;
};

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, startIcon, endIcon, error = false, ...props }, ref) => {
    return (
      <div className="relative w-full">
        {startIcon && (
          <div className="absolute top-1/2 left-3 -translate-y-1/2 text-secondary-400 dark:text-dark-secondary-400">
            {startIcon}
          </div>
        )}
        <input
          ref={ref}
          className={cn(
            "bg-surface-50 text-text w-full rounded-lg border px-4 py-2.5",
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
          <div className="absolute top-1/2 right-3 -translate-y-1/2 text-secondary-400 dark:text-dark-secondary-400">
            {endIcon}
          </div>
        )}
      </div>
    );
  },
);
Input.displayName = "Input";

export const PasswordInput = forwardRef<HTMLInputElement, InputProps>(
  ({ endIcon, ...props }, ref) => {
    const [showPassword, setShowPassword] = useState(false);

    const toggleShowPassword = (e: React.MouseEvent<HTMLButtonElement>) => {
      e.preventDefault();
      setShowPassword((prev) => !prev);
    };

    const toggleIcon = (
      <button type="button" onClick={toggleShowPassword}>
        {showPassword ? "Hide" : "Show"}
      </button>
    );

    return (
      <Input
        {...props}
        ref={ref}
        type={showPassword ? "text" : "password"}
        endIcon={toggleIcon}
      />
    );
  },
);

PasswordInput.displayName = "PasswordInput";
