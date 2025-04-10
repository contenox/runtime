// components/Dropdown.tsx
import React, { useState, useEffect, useRef } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "../utils";
import { Button } from "./Button";

export interface DropdownProps {
  // Controlled state (optional)
  isOpen?: boolean;
  onToggle?: (isOpen: boolean) => void;
  // Custom trigger element must support an onClick prop.
  trigger?: React.ReactElement<{ onClick?: React.MouseEventHandler<any> }>;
  // If provided, renders a list of options (using a button for each option).
  options?: { value: string; label: string }[];
  // Current selected value (for options mode).
  value?: string;
  // Callback when an option is selected.
  onChange?: (value: string) => void;
  // If provided, this is used as the dropdown content.
  children?: React.ReactNode;
  // Additional classes for the dropdown content container.
  contentClassName?: string;
  // Additional classes for the overall container.
  className?: string;
}

export function Dropdown({
  isOpen: controlledOpen,
  onToggle,
  trigger,
  options,
  value,
  onChange,
  children,
  contentClassName,
  className,
}: DropdownProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const isControlled = controlledOpen !== undefined;
  const isOpen = isControlled ? controlledOpen : internalOpen;

  const toggle = () => {
    if (!isControlled) setInternalOpen(!isOpen);
    onToggle?.(!isOpen);
  };

  const close = () => {
    if (!isControlled) setInternalOpen(false);
    onToggle?.(false);
  };

  // Close dropdown when clicking outside.
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(event.target as Node)
      ) {
        close();
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  // Create a trigger element if one isn’t provided.
  const triggerElement = trigger ? (
    React.cloneElement(trigger, {
      onClick: (e: React.MouseEvent) => {
        e.stopPropagation();
        // Safe to access onClick because of our type definition.
        trigger.props.onClick?.(e);
        toggle();
      },
    })
  ) : options ? (
    <Button
      onClick={toggle}
      className={cn(
        "border-secondary-300 bg-surface-50 flex w-full items-center justify-between rounded-lg border px-4 py-2.5",
        "focus:ring-primary-500 focus:ring-2 focus:ring-offset-2",
        "dark:border-dark-secondary-300 dark:bg-dark-surface-50",
      )}
    >
      <span className="text-text dark:text-dark-text">
        {options.find((opt) => opt.value === value)?.label || "Select"}
      </span>
      <ChevronDown className="text-secondary-400 dark:text-dark-secondary-400 h-5 w-5" />
    </Button>
  ) : null;

  // Determine what to render as the dropdown content.
  const content = children
    ? children
    : options
      ? options.map((option) => (
          <Button
            key={option.value}
            onClick={() => {
              onChange?.(option.value);
              close();
            }}
            className={cn(
              "text-text hover:bg-secondary-100 w-full px-4 py-2 text-left",
              "dark:text-dark-text dark:hover:bg-dark-surface-100",
              option.value === value &&
                "bg-primary-50 dark:bg-dark-primary-900",
            )}
          >
            {option.label}
          </Button>
        ))
      : null;

  return (
    <div className={cn("relative", className)} ref={dropdownRef}>
      {triggerElement}
      {isOpen && (
        <div
          className={cn(
            "absolute z-50 mt-2 w-full rounded-lg border shadow-lg",
            contentClassName,
          )}
        >
          {content}
        </div>
      )}
    </div>
  );
}
