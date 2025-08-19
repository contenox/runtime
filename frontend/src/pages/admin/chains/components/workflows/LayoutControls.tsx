import { Button } from '@contenox/ui';
import { LayoutGrid, LayoutList } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { LayoutDirection } from './utils';

interface LayoutControlsProps {
  direction: LayoutDirection;
  onChangeDirection: (dir: LayoutDirection) => void;
}

const LayoutControls: React.FC<LayoutControlsProps> = ({ direction, onChangeDirection }) => {
  const { t } = useTranslation();

  return (
    <div className="flex gap-1 rounded-md border p-1">
      <Button
        size="icon"
        variant={direction === 'horizontal' ? 'primary' : 'ghost'}
        onClick={() => onChangeDirection('horizontal')}
        aria-label={t('workflow.horizontal_layout')}>
        <LayoutGrid className="h-4 w-4" />
      </Button>
      <Button
        size="icon"
        variant={direction === 'vertical' ? 'primary' : 'ghost'}
        onClick={() => onChangeDirection('vertical')}
        aria-label={t('workflow.vertical_layout')}>
        <LayoutList className="h-4 w-4" />
      </Button>
    </div>
  );
};

export default LayoutControls;
