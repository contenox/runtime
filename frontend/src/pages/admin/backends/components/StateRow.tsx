import { Span, Table, TableCell, TableRow } from '@contenox/ui';
import { t } from 'i18next';
import { CapturedStateUnit } from '../../../../lib/types';

interface StateRowProps {
  state: CapturedStateUnit[];
}

export default function StateRow({ state }: StateRowProps) {
  return (
    <div className="overflow-auto">
      <Table
        columns={[
          t('taskstate.task_id'),
          t('taskstate.task_type'),
          t('taskstate.transition'),
          t('taskstate.duration'),
          t('taskstate.error'),
        ]}>
        {state.map((unit, index) => (
          <TableRow key={index}>
            <TableCell>
              <Span>{unit.taskID}</Span>
            </TableCell>
            <TableCell>
              <Span>{unit.taskType}</Span>
            </TableCell>
            <TableCell>
              <Span>{unit.transition}</Span>
            </TableCell>
            <TableCell>
              <Span>{unit.duration} ms</Span>
            </TableCell>
            <TableCell>
              {unit.error.error ? (
                <Span variant="status" className="text-error">
                  {unit.error.error}
                </Span>
              ) : (
                <Span variant="status" className="text-success">
                  {t('taskstate.none')}
                </Span>
              )}
            </TableCell>
          </TableRow>
        ))}
      </Table>
    </div>
  );
}
