import { cn } from "../utils";
import { Button } from "./Button";
import { Span } from "./Typography";

type Tab = {
  id: string;
  label: string;
};

type TabsProps = {
  tabs: Tab[];
  activeTab: string;
  onTabChange: (tabId: string) => void;
  className?: string;
};

export function Tabs({ tabs, activeTab, onTabChange, className }: TabsProps) {
  return (
    <div className={cn("flex gap-1", className)}>
      {tabs.map((tab) => (
        <Button
          key={tab.id}
          onClick={() => onTabChange(tab.id)}
          variant="ghost"
          textAlign="bottom"
          className={cn(
            "relative px-5 py-2.5 transition-all",
            "rounded-none",
            "hover:ring-2",
            activeTab === tab.id
              ? cn(
                  "border-b-2 border-b-primary-400 dark:border-b-dark-primary-400",
                )
              : cn(""),
          )}
        >
          <Span className={cn("relative transition-colors")}>{tab.label}</Span>
        </Button>
      ))}
    </div>
  );
}
