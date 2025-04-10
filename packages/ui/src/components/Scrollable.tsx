// components/Scrollable.tsx
import { forwardRef } from "react";
import { cn } from "../utils";

interface ScrollableProps extends React.HTMLAttributes<HTMLDivElement> {
  orientation?: "vertical" | "horizontal" | "both";
}

export const Scrollable = forwardRef<HTMLDivElement, ScrollableProps>(
  ({ className, orientation = "vertical", ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        "scrollbar-track-surface-100 scrollbar-thumb-surface-300",
        "dark:scrollbar-track-surface-800 dark:scrollbar-thumb-surface-600",
        "scrollbar-thin scrollbar-thumb-rounded hover:scrollbar-thumb-surface-400",
        "dark:hover:scrollbar-thumb-surface-500",
        {
          "overflow-y-auto": orientation === "vertical",
          "overflow-x-auto": orientation === "horizontal",
          "overflow-auto": orientation === "both",
        },
        className,
      )}
      {...props}
    />
  ),
);
Scrollable.displayName = "Scrollable";
