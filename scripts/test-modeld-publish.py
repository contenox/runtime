import copy
import hashlib
import importlib.util
import io
import json
import pathlib
import tarfile
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location('publisher', pathlib.Path(__file__).with_name('modeld-publish.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class _Store:
    def __init__(self):
        self.objects = {}
        self.index = {'schema': 1, 'builds': [{'version': 'v1.0.0', 'platform': 'linux-amd64'}]}
        self.token = 0
        self.conflict = False
        self.always_conflict = False

    def immutable(self, key, path):
        content = path.read_bytes()
        if key in self.objects and self.objects[key] != content:
            raise ValueError('immutable object collision')
        self.objects[key] = content

    def read(self, key):
        return json.dumps(self.index).encode(), self.token

    def compare_and_swap(self, key, data, token):
        if self.always_conflict:
            return False
        if self.conflict:
            self.index['builds'].append({'version': 'v1.0.0', 'platform': 'windows-amd64'})
            self.token += 1
            self.conflict = False
        if self.token != token:
            return False
        self.index = json.loads(data)
        self.token += 1
        return True


class PublishTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.archive = pathlib.Path(self.temporary.name) / 'modeld-v2.0.0-darwin-arm64.tar.gz'
        manifest = {'modeld_version': 'v2.0.0', 'platform': 'darwin-arm64', 'protocol': 1, 'backends': ['llama']}
        content = json.dumps(manifest).encode()
        with tarfile.open(self.archive, 'w:gz') as bundle:
            info = tarfile.TarInfo('modeld-v2.0.0-darwin-arm64/manifest.json')
            info.size = len(content)
            bundle.addfile(info, io.BytesIO(content))
        self.metadata = {'version': 'v2.0.0', 'platform': 'darwin-arm64', 'protocol': 1,
                         'backends': ['llama'], 'channel': 'stable',
                         'archive': 'v2.0.0/' + self.archive.name,
                         'sha256': 'v2.0.0/' + self.archive.name + '.sha256',
                         'size': self.archive.stat().st_size}
        pathlib.Path(str(self.archive) + '.build.json').write_text(json.dumps(self.metadata))
        self.checksum = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        pathlib.Path(str(self.archive) + '.sha256').write_text(f'{self.checksum}  {self.archive.name}\n')

    def test_validates_real_archive_manifest_and_checksum(self):
        self.assertEqual(publisher._validate(self.archive), (self.metadata, self.checksum))
        self.archive.write_bytes(b'corrupted')
        with self.assertRaisesRegex(ValueError, 'checksum'):
            publisher._validate(self.archive)

    def test_wrong_manifest_rejected(self):
        self.metadata['protocol'] = 2
        pathlib.Path(str(self.archive) + '.build.json').write_text(json.dumps(self.metadata))
        with self.assertRaisesRegex(ValueError, 'manifest'):
            publisher._validate(self.archive)

    def test_adds_late_platform_without_losing_concurrent_platform(self):
        store = _Store()
        store.conflict = True
        publisher._publish(store, self.archive, self.metadata, lambda: None)
        self.assertEqual({entry['platform'] for entry in store.index['builds']},
                         {'linux-amd64', 'windows-amd64', 'darwin-arm64'})
        self.assertTrue(any(key.startswith('index-history/') for key in store.objects))
        token = store.token
        publisher._publish(store, self.archive, self.metadata, lambda: None)
        self.assertEqual(store.token, token)

    def test_collision_does_not_change_index(self):
        store = _Store()
        before = copy.deepcopy(store.index)
        store.objects[self.metadata['archive']] = b'other release'
        with self.assertRaisesRegex(ValueError, 'collision'):
            publisher._publish(store, self.archive, self.metadata, lambda: None)
        self.assertEqual(store.index, before)

    def test_unreadable_public_object_does_not_change_index(self):
        store = _Store()
        before = copy.deepcopy(store.index)

        def unreadable():
            raise RuntimeError('public read denied')

        with self.assertRaisesRegex(RuntimeError, 'public read'):
            publisher._publish(store, self.archive, self.metadata, unreadable)
        self.assertEqual(store.index, before)

    def test_repeated_race_fails_without_clobber(self):
        store = _Store()
        before = copy.deepcopy(store.index)
        store.always_conflict = True
        with self.assertRaisesRegex(RuntimeError, 'index kept changing'):
            publisher._publish(store, self.archive, self.metadata, lambda: None)
        self.assertEqual(store.index, before)


class MultipartTest(unittest.TestCase):
    def test_existing_object_with_sha256_metadata_skips_upload(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'existing artifact')
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            store = publisher._S3('s3://bucket/releases')
            response = {'ContentLength': path.stat().st_size, 'Metadata': {'sha256': digest}}

            with mock.patch.object(store, '_command', return_value=publisher.subprocess.CompletedProcess(
                    [], 0, json.dumps(response), '')) as command:
                store.immutable('artifact', path)

            command.assert_called_once_with('head-object', 'artifact', '--checksum-mode', 'ENABLED')

    def test_signed_manifest_accepts_existing_s3_checked_object(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'existing artifact')
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            store = publisher._S3('s3://bucket/releases')
            response = {'ContentLength': path.stat().st_size, 'Metadata': {},
                        'ChecksumCRC64NVME': 'stored-full-object-checksum'}

            with mock.patch.object(store, '_command', return_value=publisher.subprocess.CompletedProcess(
                    [], 0, json.dumps(response), '')) as command:
                store.immutable_from_manifest('artifact', path, digest)

            command.assert_called_once_with('head-object', 'artifact', '--checksum-mode', 'ENABLED')

    def test_large_existing_object_without_verification_metadata_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'unverifiable')
            store = publisher._S3('s3://bucket/releases')
            response = {'ContentLength': path.stat().st_size, 'Metadata': {}}

            with mock.patch.object(publisher, 'VERIFY_DOWNLOAD_LIMIT', 1), \
                    mock.patch.object(store, '_command', return_value=publisher.subprocess.CompletedProcess(
                        [], 0, json.dumps(response), '')) as command:
                with self.assertRaisesRegex(RuntimeError, 'large download'):
                    store.immutable('artifact', path)

            command.assert_called_once_with('head-object', 'artifact', '--checksum-mode', 'ENABLED')

    def test_uploads_parts_concurrently_and_completes_immutably(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'abcdefghijklmnop')
            store = publisher._S3('s3://bucket/releases')
            uploaded = {}
            completed = []

            def command(operation, key, *args):
                if operation == 'create-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 0, '{"UploadId":"upload"}', '')
                if operation == 'upload-part':
                    number = int(args[args.index('--part-number') + 1])
                    body = pathlib.Path(args[args.index('--body') + 1])
                    uploaded[number] = body.read_bytes()
                    return publisher.subprocess.CompletedProcess([], 0, json.dumps({'ETag': f'etag-{number}'}), '')
                if operation == 'complete-multipart-upload':
                    self.assertEqual(args[-2:], ('--if-none-match', '*'))
                    completed.extend(json.loads(args[args.index('--multipart-upload') + 1])['Parts'])
                    return publisher.subprocess.CompletedProcess([], 0, '{}', '')
                self.fail(f'unexpected operation: {operation}')

            with mock.patch.object(publisher, 'MULTIPART_PART_SIZE', 5), \
                    mock.patch.object(publisher, 'MULTIPART_WORKERS', 2), \
                    mock.patch.object(store, '_command', side_effect=command):
                result = store._multipart_put('artifact', path, hashlib.sha256(path.read_bytes()).hexdigest())

            self.assertEqual(result.returncode, 0)
            self.assertEqual(b''.join(uploaded[number] for number in sorted(uploaded)), path.read_bytes())
            self.assertEqual([part['PartNumber'] for part in completed], [1, 2, 3, 4])

    def test_aborts_failed_multipart_upload(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'abcdefgh')
            store = publisher._S3('s3://bucket/releases')
            operations = []

            def command(operation, key, *args):
                operations.append(operation)
                if operation == 'create-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 0, '{"UploadId":"upload"}', '')
                if operation == 'upload-part':
                    return publisher.subprocess.CompletedProcess([], 1, '', 'part failed')
                if operation == 'abort-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 0, '{}', '')
                self.fail(f'unexpected operation: {operation}')

            with mock.patch.object(publisher, 'MULTIPART_PART_SIZE', 5), \
                    mock.patch.object(store, '_command', side_effect=command):
                with self.assertRaisesRegex(RuntimeError, 'part failed'):
                    store._multipart_put('artifact', path, hashlib.sha256(path.read_bytes()).hexdigest())

            self.assertIn('abort-multipart-upload', operations)

    def test_aborts_when_an_immutable_object_wins_completion(self):
        with tempfile.TemporaryDirectory() as temporary_name:
            path = pathlib.Path(temporary_name) / 'artifact'
            path.write_bytes(b'abcdefgh')
            store = publisher._S3('s3://bucket/releases')
            operations = []

            def command(operation, key, *args):
                operations.append(operation)
                if operation == 'create-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 0, '{"UploadId":"upload"}', '')
                if operation == 'upload-part':
                    return publisher.subprocess.CompletedProcess([], 0, '{"ETag":"etag"}', '')
                if operation == 'complete-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 1, '', '(PreconditionFailed)')
                if operation == 'abort-multipart-upload':
                    return publisher.subprocess.CompletedProcess([], 0, '{}', '')
                self.fail(f'unexpected operation: {operation}')

            with mock.patch.object(publisher, 'MULTIPART_PART_SIZE', 5), \
                    mock.patch.object(store, '_command', side_effect=command):
                result = store._multipart_put('artifact', path, hashlib.sha256(path.read_bytes()).hexdigest())

            self.assertEqual(result.returncode, 1)
            self.assertEqual(operations.count('abort-multipart-upload'), 1)


if __name__ == '__main__':
    unittest.main()
