import argparse
import base64
import concurrent.futures
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile


MULTIPART_THRESHOLD = 64 * 1024 * 1024
MULTIPART_PART_SIZE = 64 * 1024 * 1024
MULTIPART_WORKERS = 4
VERIFY_DOWNLOAD_LIMIT = 16 * 1024 * 1024


def _aws_executable():
    executable = shutil.which('aws')
    if executable:
        return executable
    program_files = os.environ.get('ProgramFiles')
    if program_files:
        executable = pathlib.Path(program_files) / 'Amazon' / 'AWSCLIV2' / 'aws.exe'
        if executable.is_file():
            return str(executable)
    raise FileNotFoundError('AWS CLI is not installed or available on PATH')


def _digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def _run_aws(command, upload_key=None):
    if upload_key is None:
        return subprocess.run(command, capture_output=True, text=True)
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    line = ''
    try:
        if not sys.stderr.isatty():
            print(f'Uploading {upload_key}...', file=sys.stderr, flush=True)
        else:
            frames = '|/-\\'
            frame = 0
            while process.poll() is None:
                line = f'Uploading {upload_key} {frames[frame % len(frames)]}'
                sys.stderr.write('\r' + line)
                sys.stderr.flush()
                frame += 1
                time.sleep(0.15)
        stdout, stderr = process.communicate()
    except BaseException:
        process.terminate()
        process.wait()
        raise
    finally:
        if line:
            sys.stderr.write('\r' + (' ' * len(line)) + '\r')
            sys.stderr.flush()
    return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)


def _validate(archive):
    metadata = json.loads(pathlib.Path(str(archive) + '.build.json').read_text())
    version, platform = metadata['version'], metadata['platform']
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?', version):
        raise ValueError('invalid worker version')
    if not re.fullmatch(r'(linux|darwin|windows)-(amd64|arm64)', platform):
        raise ValueError('invalid worker platform')
    extension = '.zip' if platform.startswith('windows-') else '.tar.gz'
    name = f'modeld-{version}-{platform}'
    if archive.name != name + extension:
        raise ValueError('archive filename disagrees with metadata')
    if metadata['archive'] != f'{version}/{archive.name}' or metadata['sha256'] != f'{version}/{archive.name}.sha256':
        raise ValueError('metadata paths do not match the release layout')
    if metadata['channel'] != 'stable' or type(metadata['protocol']) is not int or metadata['protocol'] < 1:
        raise ValueError('invalid channel or protocol')
    if not metadata['backends'] or not set(metadata['backends']) <= {'llama', 'openvino'}:
        raise ValueError('invalid backend set')
    checksum = pathlib.Path(str(archive) + '.sha256')
    fields = checksum.read_text().strip().split()
    if len(fields) != 2 or fields[1].lstrip('*') != archive.name or fields[0] != _digest(archive):
        raise ValueError('archive checksum mismatch')
    if metadata['size'] != archive.stat().st_size:
        raise ValueError('archive size mismatch')
    if extension == '.zip':
        with zipfile.ZipFile(archive) as bundle:
            manifest = json.loads(bundle.read(name + '/manifest.json'))
    else:
        with tarfile.open(archive) as bundle:
            manifest = json.load(bundle.extractfile(name + '/manifest.json'))
    for field, manifest_field in [('version', 'modeld_version'), ('platform', 'platform'),
                                  ('protocol', 'protocol'), ('backends', 'backends')]:
        if metadata[field] != manifest[manifest_field]:
            raise ValueError(f'manifest disagrees with metadata: {field}')
    return metadata, fields[0]


class _S3:
    def __init__(self, uri):
        match = re.fullmatch(r's3://([^/]+)/(.+)', uri.rstrip('/'))
        if not match:
            raise ValueError('release store must be s3://bucket/prefix')
        self.bucket, self.prefix = match.groups()

    def _command(self, operation, key, *args):
        command = [_aws_executable(), 's3api', operation, '--bucket', self.bucket,
                   '--key', self.prefix + '/' + key, *args,
                   '--output', 'json', '--no-cli-pager']
        return _run_aws(command, key if operation == 'put-object' else None)

    def read(self, key):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'object'
            result = self._command('get-object', key, str(path))
            if result.returncode:
                if '(NoSuchKey)' in result.stderr or '(404)' in result.stderr:
                    return None, None
                raise RuntimeError(result.stderr)
            return path.read_bytes(), json.loads(result.stdout)['ETag']

    def _upload_part(self, key, path, upload_id, part_number, offset, size, temporary):
        part = temporary / f'part-{part_number}'
        with path.open('rb') as source, part.open('wb') as target:
            source.seek(offset)
            remaining = size
            while remaining:
                data = source.read(min(1024 * 1024, remaining))
                if not data:
                    raise EOFError(f'unexpected end of upload source: {path}')
                target.write(data)
                remaining -= len(data)
        try:
            result = self._command('upload-part', key, '--part-number', str(part_number),
                                   '--body', str(part), '--upload-id', upload_id)
            if result.returncode:
                raise RuntimeError(result.stderr)
            return {'ETag': json.loads(result.stdout)['ETag'], 'PartNumber': part_number}, size
        finally:
            part.unlink(missing_ok=True)

    def _multipart_put(self, key, path, digest):
        created = self._command('create-multipart-upload', key, '--metadata', f'sha256={digest}')
        if created.returncode:
            raise RuntimeError(created.stderr)
        upload_id = json.loads(created.stdout)['UploadId']
        total = path.stat().st_size
        count = (total + MULTIPART_PART_SIZE - 1) // MULTIPART_PART_SIZE
        completed = []
        uploaded = 0
        started = time.monotonic()
        line = ''
        try:
            print(f'Uploading {key} in {count} parts with {MULTIPART_WORKERS} workers...',
                  file=sys.stderr, flush=True)
            with tempfile.TemporaryDirectory() as temporary_name:
                temporary = pathlib.Path(temporary_name)
                with concurrent.futures.ThreadPoolExecutor(max_workers=MULTIPART_WORKERS) as executor:
                    futures = []
                    for index in range(count):
                        offset = index * MULTIPART_PART_SIZE
                        size = min(MULTIPART_PART_SIZE, total - offset)
                        futures.append(executor.submit(self._upload_part, key, path, upload_id,
                                                       index + 1, offset, size, temporary))
                    for future in concurrent.futures.as_completed(futures):
                        part, size = future.result()
                        completed.append(part)
                        uploaded += size
                        elapsed = max(time.monotonic() - started, 0.001)
                        rate = uploaded / elapsed / (1024 * 1024)
                        line = (f'Uploading {key}: {uploaded / total:6.1%} '
                                f'({uploaded / (1024 * 1024):.0f}/{total / (1024 * 1024):.0f} MiB, '
                                f'{rate:.1f} MiB/s)')
                        if sys.stderr.isatty():
                            sys.stderr.write('\r' + line)
                            sys.stderr.flush()
                        else:
                            print(line, file=sys.stderr, flush=True)
            complete = self._command('complete-multipart-upload', key, '--upload-id', upload_id,
                                     '--multipart-upload', json.dumps({'Parts': sorted(
                                         completed, key=lambda part: part['PartNumber'])}),
                                     '--if-none-match', '*')
            if complete.returncode:
                if ('(PreconditionFailed)' not in complete.stderr and
                        '(ConditionalRequestConflict)' not in complete.stderr):
                    raise RuntimeError(complete.stderr)
                aborted = self._command('abort-multipart-upload', key, '--upload-id', upload_id)
                if aborted.returncode:
                    raise RuntimeError(f'{complete.stderr}\nfailed to abort multipart upload: {aborted.stderr}')
                return complete
            return complete
        except BaseException as error:
            aborted = self._command('abort-multipart-upload', key, '--upload-id', upload_id)
            if aborted.returncode:
                raise RuntimeError(f'{error}\nfailed to abort multipart upload: {aborted.stderr}') from error
            raise
        finally:
            if line and sys.stderr.isatty():
                sys.stderr.write('\r' + (' ' * len(line)) + '\r')
                sys.stderr.flush()

    def _existing(self, key):
        result = self._command('head-object', key, '--checksum-mode', 'ENABLED')
        if result.returncode:
            if '(NoSuchKey)' in result.stderr or '(404)' in result.stderr or 'Not Found' in result.stderr:
                return None
            raise RuntimeError(result.stderr)
        return json.loads(result.stdout)

    def _verify_existing(self, key, path, digest, manifest_verified):
        existing = self._existing(key)
        if existing is None:
            return False
        if existing.get('ContentLength') != path.stat().st_size:
            raise ValueError(f'refusing to replace published object with a different size: {key}')
        stored_digest = existing.get('Metadata', {}).get('sha256')
        if stored_digest:
            if stored_digest != digest:
                raise ValueError(f'refusing to replace published object with a different checksum: {key}')
            return True
        stored_checksums = [value for name, value in existing.items() if name.startswith('Checksum')
                            and name != 'ChecksumType' and value]
        if manifest_verified and stored_checksums:
            return True
        if path.stat().st_size > VERIFY_DOWNLOAD_LIMIT:
            raise RuntimeError(f'published object has no SHA-256 metadata and cannot be verified without a large download: {key}')
        with tempfile.TemporaryDirectory() as temporary:
            downloaded = pathlib.Path(temporary) / 'object'
            result = self._command('get-object', key, str(downloaded))
            if result.returncode:
                raise RuntimeError(result.stderr)
            if _digest(downloaded) != digest:
                raise ValueError(f'refusing to replace published object: {key}')
        return True

    def _immutable(self, key, path, digest, manifest_verified):
        if self._verify_existing(key, path, digest, manifest_verified):
            return
        if path.stat().st_size >= MULTIPART_THRESHOLD:
            result = self._multipart_put(key, path, digest)
        else:
            checksum = base64.b64encode(bytes.fromhex(digest)).decode()
            result = self._command('put-object', key, '--body', str(path), '--metadata',
                                   f'sha256={digest}', '--checksum-sha256', checksum,
                                   '--if-none-match', '*')
        if result.returncode:
            if '(PreconditionFailed)' not in result.stderr and '(ConditionalRequestConflict)' not in result.stderr:
                raise RuntimeError(result.stderr)
            if not self._verify_existing(key, path, digest, manifest_verified):
                raise RuntimeError(f'published object disappeared after an immutable write conflict: {key}')

    def immutable(self, key, path):
        self._immutable(key, path, _digest(path), False)

    def immutable_from_manifest(self, key, path, digest):
        if not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('manifest contains an invalid SHA-256 digest')
        self._immutable(key, path, digest, True)

    def compare_and_swap(self, key, data, token):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'index.json'
            path.write_bytes(data)
            condition = ['--if-none-match', '*'] if token is None else ['--if-match', token]
            result = self._command('put-object', key, '--body', str(path), '--content-type',
                                   'application/json', '--cache-control', 'no-cache', *condition)
            if result.returncode:
                if '(PreconditionFailed)' in result.stderr or '(ConditionalRequestConflict)' in result.stderr:
                    return False
                raise RuntimeError(result.stderr)
            return True


def _public_check(base_url, metadata, checksum):
    if not base_url.startswith('https://'):
        raise ValueError('public base URL must use HTTPS')
    request = urllib.request.Request(base_url.rstrip('/') + '/' + metadata['archive'], method='HEAD')
    with urllib.request.urlopen(request, timeout=30) as response:
        if int(response.headers['Content-Length']) != metadata['size']:
            raise ValueError('public archive size differs from local artifact')
    with urllib.request.urlopen(base_url.rstrip('/') + '/' + metadata['sha256'], timeout=30) as response:
        if response.read(4096).decode().split()[0] != checksum:
            raise ValueError('public checksum differs from local artifact')


def _publish(store, archive, metadata, public_check):
    store.immutable(metadata['archive'], archive)
    store.immutable(metadata['sha256'], pathlib.Path(str(archive) + '.sha256'))
    store.immutable(metadata['archive'] + '.build.json', pathlib.Path(str(archive) + '.build.json'))
    public_check()
    for attempt in range(8):
        data, token = store.read('index.json')
        index = {'schema': 1, 'builds': []} if data is None else json.loads(data)
        if index.get('schema') != 1 or not isinstance(index.get('builds'), list):
            raise ValueError('unsupported existing release index')
        matches = [entry for entry in index['builds'] if
                   (entry['version'], entry['platform']) == (metadata['version'], metadata['platform'])]
        if matches:
            if len(matches) == 1 and matches[0] == metadata:
                return
            raise ValueError('version/platform already indexed with different metadata')
        index['builds'].append(metadata)
        index['builds'].sort(key=lambda entry: (entry['version'], entry['platform']))
        if data is not None:
            with tempfile.TemporaryDirectory() as temporary:
                previous = pathlib.Path(temporary) / 'previous.json'
                previous.write_bytes(data)
                store.immutable('index-history/' + hashlib.sha256(data).hexdigest() + '.json', previous)
        if store.compare_and_swap('index.json', (json.dumps(index, indent=2) + '\n').encode(), token):
            return
    raise RuntimeError('index kept changing; immutable artifacts uploaded, index not updated; retry publication')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Publish an immutable worker and merge it into the installer index.')
    parser.add_argument('archive', type=pathlib.Path)
    parser.add_argument('--store')
    parser.add_argument('--public-base-url')
    parser.add_argument('--check-only', action='store_true')
    args = parser.parse_args()
    metadata, checksum = _validate(args.archive)
    if args.check_only:
        print(json.dumps(metadata, indent=2))
    else:
        if not args.store or not args.public_base_url:
            parser.error('--store and --public-base-url are required for publication')
        if not args.public_base_url.startswith('https://'):
            parser.error('--public-base-url must use HTTPS')
        _publish(_S3(args.store), args.archive, metadata,
                 lambda: _public_check(args.public_base_url, metadata, checksum))
        print(f'Published {metadata["platform"]} {metadata["version"]}: {args.public_base_url.rstrip("/")}/index.json')
