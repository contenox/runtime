import {
  Button,
  EmptyState,
  Form,
  FormField,
  GridLayout,
  H2,
  Input,
  Panel,
  Scrollable,
  Section,
  Spinner,
  Table,
  TableCell,
  TableRow,
} from '@cate/ui';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCreateFile, useDeleteFile, useListFiles } from '../../../hooks/useFiles';
import { api } from '../../../lib/api';

function formatBytes(bytes: number, decimals = 2): string {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

export default function FilesPage() {
  const { t } = useTranslation();
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [uploadPath, setUploadPath] = useState('');
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const { data: files, isLoading: isLoadingFiles, error: filesError } = useListFiles();
  const createFileMutation = useCreateFile();
  const deleteFileMutation = useDeleteFile();

  const handleFileChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    setSelectedFile(file || null);
    if (file && !uploadPath) {
      setUploadPath(file.name);
    }
  };

  const handleUploadSubmit = (event: React.FormEvent) => {
    event.preventDefault();
    if (!selectedFile) return;

    const formData = new FormData();
    formData.append('file', selectedFile);
    formData.append('path', uploadPath);

    createFileMutation.mutate(formData, {
      onSuccess: () => {
        setSelectedFile(null);
        setUploadPath('');
      },
    });
  };

  const handleDeleteClick = (id: string) => {
    setDeletingId(id);
    deleteFileMutation.mutate(id, {
      onSettled: () => {
        setDeletingId(null);
      },
    });
  };

  const renderFileList = () => {
    if (isLoadingFiles) {
      return (
        <Section className="flex items-center justify-center py-10">
          <Spinner size="lg" />
        </Section>
      );
    }

    if (filesError) {
      return (
        <Panel variant="error" title={t('files.list_error_title')}>
          {filesError.message || t('errors.generic_fetch')}
        </Panel>
      );
    }

    if (!files || files.length === 0) {
      return (
        <EmptyState
          title={t('files.list_empty_title')}
          description={t('files.list_empty_message')}
        />
      );
    }

    return (
      <Table columns={[t('common.path'), t('common.type'), t('common.size'), t('common.actions')]}>
        {files.map(file => {
          const isDeleting = deletingId === file.id;
          return (
            <TableRow key={file.id}>
              <TableCell className="break-all">{file.path}</TableCell>
              <TableCell>{file.contentType}</TableCell>
              <TableCell>{formatBytes(file.size)}</TableCell>
              <TableCell className="space-x-2">
                <Button
                  variant="accent"
                  size="sm"
                  onClick={() => handleDeleteClick(file.id)}
                  disabled={isDeleting || deleteFileMutation.isPending}>
                  {isDeleting ? <Spinner size="sm" /> : t('common.delete')}
                </Button>
                <a href={api.getDownloadFileUrl(file.id)} download={file.path}>
                  <Button variant="secondary" size="sm">
                    {t('common.download')}
                  </Button>
                </a>
              </TableCell>
            </TableRow>
          );
        })}
      </Table>
    );
  };

  return (
    <GridLayout variant="body">
      <Section className="overflow-hidden">
        <H2 className="mb-4">{t('files.list_title')}</H2>
        <Scrollable orientation="vertical">{renderFileList()}</Scrollable>
      </Section>

      <Section>
        <Form
          title={t('files.upload_title')}
          onSubmit={handleUploadSubmit}
          error={
            createFileMutation.isError
              ? createFileMutation.error?.message || t('errors.generic_upload')
              : undefined
          }
          actions={
            <Button
              type="submit"
              variant="primary"
              disabled={!selectedFile || createFileMutation.isPending}>
              {createFileMutation.isPending ? <Spinner size="sm" /> : t('files.upload_action')}
            </Button>
          }>
          <FormField label={t('files.form_select_file')} required>
            <Input type="file" onChange={handleFileChange} />
          </FormField>
          <FormField label={t('files.form_path')}>
            <Input
              value={uploadPath}
              onChange={e => setUploadPath(e.target.value)}
              placeholder={t('files.form_path_placeholder')}
            />
          </FormField>
        </Form>
      </Section>
    </GridLayout>
  );
}
