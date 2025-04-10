// components/EmptyState.tsx
import { cn } from "../utils";
import { H3, P } from "./Typography";

type EmptyStateProps = {
  title: string;
  subtitle?: string;
  description: string;
  icon?: React.ReactNode;
  className?: string;
  orientation?: "vertical" | "horizontal";
  iconSize?: "sm" | "md" | "lg";
};

export function EmptyState({
  title,
  subtitle, // New subtitle
  description,
  icon,
  className,
  orientation = "vertical",
  iconSize = "md",
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "p-8",
        orientation === "horizontal"
          ? "flex items-center gap-6 text-left"
          : "text-center",
        className,
      )}
    >
      {icon && (
        <div
          className={cn(
            "text-primary dark:text-dark-primary",
            orientation === "horizontal" ? "flex-shrink-0" : "mx-auto",
            {
              "text-3xl": iconSize === "lg",
              "text-2xl": iconSize === "md",
              "text-xl": iconSize === "sm",
            },
          )}
        >
          {icon}
        </div>
      )}
      <div className={cn(orientation === "horizontal" && "flex-1")}>
        <H3
          variant={orientation === "horizontal" ? undefined : "cardTitle"}
          className="mb-2"
        >
          {title}
        </H3>
        {/* New subtitle section */}
        {subtitle && (
          <P
            variant="lead"
            className="mb-4 text-text-muted dark:text-dark-text-muted"
          >
            {subtitle}
          </P>
        )}
        <P variant={orientation === "horizontal" ? undefined : "cardSubtitle"}>
          {description}
        </P>
      </div>
    </div>
  );
}
