import React, { useState, useEffect } from "react";
import { Tabs } from "./Tabs";

export interface Tab {
  id: string;
  label: string;
  content: React.ReactNode;
}

interface TabbedPageProps extends React.HTMLAttributes<HTMLDivElement> {
  tabs: Tab[];
  defaultActiveTab?: string;
  activeTab?: string;
  onTabChange?: (tabId: string) => void;
}

export function TabbedPage({
  tabs,
  defaultActiveTab,
  activeTab: controlledActiveTab,
  onTabChange,
  ...props
}: TabbedPageProps) {
  const [activeTab, setActiveTab] = useState(
    controlledActiveTab || defaultActiveTab || tabs[0].id,
  );

  useEffect(() => {
    if (
      controlledActiveTab !== undefined &&
      controlledActiveTab !== activeTab
    ) {
      setActiveTab(controlledActiveTab);
    }
  }, [controlledActiveTab, activeTab]);

  const handleTabChange = (newTab: string) => {
    setActiveTab(newTab);
    if (onTabChange) {
      onTabChange(newTab);
    }
  };

  return (
    <div {...props}>
      <Tabs
        tabs={tabs.map(({ id, label }) => ({ id, label }))}
        activeTab={activeTab}
        onTabChange={handleTabChange}
      />
      <div className="mt-4">
        {tabs.map(({ id, content }) => (
          <div key={id} className={id === activeTab ? "block" : "hidden"}>
            {content}
          </div>
        ))}
      </div>
    </div>
  );
}
