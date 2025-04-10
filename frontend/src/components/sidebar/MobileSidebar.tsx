import { SidebarProps } from './Sidebar';
import { SidebarNav } from './SidebarNav';

export function MobileSidebar({ isOpen, setIsOpen, items }: SidebarProps) {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-x-0 top-20 bottom-0 z-50 sm:hidden">
      {/* Backdrop covering the area below the top panel */}
      <div
        className="bg-surface-100 dark:bg-dark-surface-100 fixed inset-x-0 top-20 bottom-0 z-40"
        onClick={() => setIsOpen(false)}></div>
      {/* Sidebar overlay */}
      <div className="bg-surface border-surface-300 dark:bg-dark-surface dark:border-dark-surface-300 relative z-50 flex h-full flex-col border-r shadow-lg">
        <SidebarNav items={items} setIsOpen={setIsOpen} />
      </div>
    </div>
  );
}
