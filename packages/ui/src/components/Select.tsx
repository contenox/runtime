// components/Select.tsx
import { forwardRef } from "react";
import { cn } from "../utils";

type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement> & {
  options: Array<{ value: string; label: string }>;
  placeholder?: string;
};

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, options, placeholder, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        "border-secondary-300 w-full rounded-lg border px-3 py-2",
        "bg-surface-50 focus:ring-primary-500 focus:border-transparent focus:ring-2",
        "dark:bg-dark-surface-50 dark:border-dark-secondary-300 dark:focus:ring-primary-400",
        className,
      )}
      {...props}
    >
      {placeholder && (
        <SelectOption value="" disabled hidden selected={props.value === ""}>
          {placeholder}
        </SelectOption>
      )}
      {options.map((option) => (
        <SelectOption key={option.value} value={option.value}>
          {option.label}
        </SelectOption>
      ))}
    </select>
  ),
);
Select.displayName = "Select";

type SelectOptionProps = React.OptionHTMLAttributes<HTMLOptionElement>;

export const SelectOption = forwardRef<HTMLOptionElement, SelectOptionProps>(
  ({ className, ...props }, ref) => (
    <option
      ref={ref}
      className={cn(
        "bg-surface-50 text-text",
        "dark:bg-dark-surface-50 dark:text-dark-text",
        className,
      )}
      {...props}
    />
  ),
);
SelectOption.displayName = "SelectOption";
