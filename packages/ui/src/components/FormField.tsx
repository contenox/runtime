// components/FormField.tsx
import { cn } from "../utils";
import { Label } from "./Label";

type FormFieldProps = {
  label: string;
  required?: boolean;
  error?: string;
  children: React.ReactNode;
  className?: string;
};

export function FormField({
  label,
  required,
  error,
  children,
  className,
}: FormFieldProps) {
  return (
    <div className={cn("space-y-2", className)}>
      <Label className="text-text dark:text-dark-text block text-sm font-medium">
        {label}
        {required && (
          <span className="text-error dark:text-dark-error ml-1">*</span>
        )}
      </Label>
      {children}
      {error && (
        <p className="text-error dark:text-dark-error text-sm mt-1">{error}</p>
      )}
    </div>
  );
}
