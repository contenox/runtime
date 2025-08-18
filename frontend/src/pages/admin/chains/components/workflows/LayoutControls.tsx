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
    <div className="flex gap-1 rounded-md bg-gray-100 p-1">
      <Button
        size="sm"
        variant={direction === 'horizontal' ? 'primary' : 'ghost'}
        onClick={() => onChangeDirection('horizontal')}
        title={t('workflow.horizontal_layout')}>
        <LayoutGrid className="h-4 w-4" />
      </Button>
      <Button
        size="sm"
        variant={direction === 'vertical' ? 'primary' : 'ghost'}
        onClick={() => onChangeDirection('vertical')}
        title={t('workflow.vertical_layout')}>
        <LayoutList className="h-4 w-4" />
      </Button>
    </div>
  );
};

export default LayoutControls;
