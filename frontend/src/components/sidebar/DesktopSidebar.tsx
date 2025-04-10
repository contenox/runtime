import { cn } from '../../lib/utils';
import { SidebarProps } from './Sidebar';
import { SidebarNav } from './SidebarNav';

export function DesktopSidebar({ isOpen, setIsOpen, items, className }: SidebarProps) {
  return (
    <div
      className={cn(
        'hidden transition-all duration-300 ease-in-out sm:flex sm:flex-col sm:overflow-hidden',
        isOpen ? 'border-surface-300 dark:border-dark-surface-700 w-64 border-r' : 'w-0 border-0',
        'bg-surface dark:bg-dark-surface-100 shadow-lg',
        className,
      )}>
      <div className="flex h-full w-64 flex-col">
        <SidebarNav items={items} setIsOpen={setIsOpen} />
      </div>
    </div>
  );
}
