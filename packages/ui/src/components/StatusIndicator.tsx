import { cn } from "../utils";
import { ProgressBar } from "./ProgressBar";
import { P } from "./Typography";

type Status = "planned" | "in-progress" | "completed";
type Size = "sm" | "md";

type StatusIndicatorProps = {
  status: Status;
  label?: string;
  progress?: number;
  size?: Size;
  className?: string;
};

export function StatusIndicator({
  status,
  label,
  progress,
  size = "md",
  className,
}: StatusIndicatorProps) {
  const statusConfig = {
    planned: {
      color: "bg-surface-400 dark:bg-dark-surface-500",
      text: "text-text-muted dark:text-dark-text-muted",
    },
    "in-progress": {
      color: "bg-yellow-500 dark:bg-dark-warning-500",
      text: "text-yellow-700 dark:text-dark-warning-300",
    },
    completed: {
      color: "bg-green-500 dark:bg-dark-success-500",
      text: "text-green-700 dark:text-dark-success-300",
    },
  };

  return (
    <div className={cn("flex items-center gap-3", className)}>
      {/* Status dot */}
      <span
        className={cn("w-2 h-2 rounded-full", statusConfig[status].color)}
      />

      {/* Label and progress */}
      <div className="flex-1">
        {label && (
          <P
            variant="caption"
            className={cn(
              "uppercase tracking-wide",
              statusConfig[status].text,
              size === "sm" ? "text-xs" : "text-sm",
            )}
          >
            {label}
          </P>
        )}

        {typeof progress === "number" && (
          <ProgressBar
            value={progress}
            palette={
              status === "completed"
                ? "success"
                : status === "in-progress"
                  ? "warning"
                  : "neutral"
            }
            className={size === "sm" ? "h-1.5" : "h-2"}
          />
        )}
      </div>
    </div>
  );
}
