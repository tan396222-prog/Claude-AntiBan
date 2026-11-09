"""SaveFile destinations and completed Desktop exports (runs as cage, never root)."""
import ctypes as C
from datetime import datetime, timezone
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import re
import select
import stat
import struct
import threading
import uuid


def valid_name(name):
    return (isinstance(name, str) and bool(name) and len(name.encode()) <= 255
            and name not in ('.', '..') and not re.search(r'[/\\\x00-\x1f]', name))


class Downloads:
    def __init__(self, directory, workspace, copier):
        self.directory, self.workspace, self.copier = Path(directory), workspace, copier
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.manifest = self.directory / 'state.json'
        self.lock = threading.RLock()
        self.copy_lock = threading.Lock()
        self.records = {}
        try:
            if self.manifest.stat().st_size > 1024 * 1024:
                raise ValueError('下载记录过大')
            records = json.loads(self.manifest.read_text())
            for item in records[-100:]:
                if (not re.fullmatch(r'[a-f0-9-]{36}', item['id']) or not valid_name(item['name'])
                        or not valid_name(item['sourceName'])):
                    continue
                if item['state'] in ('downloading', 'saving'):
                    item.update(state='failed', error='下载或保存被中断；已完整写入的文件可重试保存。')
                item['retryable'] = bool(item.get('complete') and self.source(item).is_file()
                                         and item['state'] == 'failed')
                self.records[item['id']] = item
        except (OSError, ValueError, TypeError, KeyError):
            self.records = {}

    def source(self, item):
        return self.directory / item['id'] / item['sourceName']

    def persist(self):
        finished = [key for key, item in self.records.items() if item['state'] in ('saved', 'failed', 'canceled')]
        for key in finished[:-50]:
            del self.records[key]
        temporary = self.manifest.with_suffix('.tmp')
        with temporary.open('w') as output:
            os.chmod(temporary, 0o600)
            json.dump(list(self.records.values()), output, ensure_ascii=False)
            output.flush()
            os.fsync(output.fileno())
        temporary.replace(self.manifest)

    def snapshot(self):
        with self.lock:
            return [{key: value for key, value in item.items() if key not in ('sourceName', 'complete')}
                    for item in reversed(list(self.records.values()))]

    def begin(self, name):
        if not valid_name(name):
            raise ValueError('下载文件名无效')
        with self.lock:
            if sum(item['state'] in ('downloading', 'saving') for item in self.records.values()) >= 50:
                raise ValueError('同时下载的文件过多，请等待下载完成')
            key = str(uuid.uuid4())
            folder = self.directory / key
            folder.mkdir(mode=0o700)
            # Watch before handing the URI to Chromium, including very small exports.
            libc = C.CDLL(None, use_errno=True)
            fd = libc.inotify_init1(os.O_CLOEXEC | os.O_NONBLOCK)
            if fd < 0:
                raise OSError(C.get_errno(), '无法监听下载完成')
            watch = libc.inotify_add_watch(fd, os.fsencode(folder), 0x2 | 0x8 | 0x80 | 0x200)
            if watch < 0:
                os.close(fd)
                raise OSError(C.get_errno(), '无法监听下载目录')
            item = dict(id=key, name=name, sourceName=name, state='downloading', target='desktop',
                        startedAt=datetime.now(timezone.utc).isoformat(), complete=False, retryable=False)
            self.records[key] = item
            try:
                self.persist()
            except Exception:
                del self.records[key]
                os.close(fd)
                raise
            threading.Thread(target=self.watch, args=(item, fd), daemon=True).start()
            return str(self.source(item))

    def watch(self, item, fd):
        wrote_target = False
        try:
            while True:
                if not select.select([fd], [], [], 1800)[0]:
                    raise ValueError('长时间未收到完整文件，请从原应用重新下载')
                data = os.read(fd, 65536)
                offset = 0
                while offset + 16 <= len(data):
                    _, mask, _, length = struct.unpack_from('iIII', data, offset)
                    name = os.fsdecode(data[offset + 16:offset + 16 + length].split(b'\0')[0])
                    offset += 16 + length
                    if mask & 0x4000:  # IN_Q_OVERFLOW: never infer completion from size stability.
                        raise ValueError('下载完成通知丢失，请重新下载')
                    if name == item['sourceName'] + '.crdownload' and mask & 0x200:
                        with self.lock:
                            item.update(state='canceled', retryable=False)
                            self.persist()
                        return
                    if name == item['sourceName'] and mask & 0x2:
                        wrote_target = True
                    # Chromium first reserves the final name with an empty file,
                    # then writes .crdownload and renames it. Its initial close
                    # is not completion. A direct exporter must actually write.
                    if name == item['sourceName'] and (mask & 0x80 or (mask & 0x8 and wrote_target)):
                        with self.lock:
                            item.update(complete=True, state='saving')
                            self.persist()
                        self.save(item)
                        return
        except Exception as error:
            self.failed(item, error)
        finally:
            os.close(fd)

    def failed(self, item, error):
        with self.lock:
            item.update(state='failed', error=str(error)[:300],
                        retryable=bool(item['complete'] and self.source(item).is_file()))
            try:
                self.persist()
            except OSError:
                item['error'] = '无法保存下载记录，请检查客机可用空间'

    def save(self, item):
        try:
            with self.copy_lock:
                source = os.open(self.source(item), os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                try:
                    info = os.fstat(source)
                    if not stat.S_ISREG(info.st_mode):
                        raise ValueError('下载源文件无效')
                    result = self.copier(source, info.st_size, self.workspace, item['sourceName'])
                finally:
                    os.close(source)
            with self.lock:
                item.update(state='saved', path=result['path'], name=result['name'],
                            receivedBytes=result['bytes'], totalBytes=result['bytes'], retryable=False)
                item.pop('error', None)
                self.persist()
            # Retain the private source: applications may reuse a SaveFile URI.
        except Exception as error:
            self.failed(item, error)

    def retry(self, key):
        with self.lock:
            item = self.records.get(key)
            if not item or item['state'] != 'failed' or not item['retryable']:
                raise ValueError('完整下载源文件不可用，请重新下载')
            item.update(state='saving', retryable=False)
            self.persist()
            threading.Thread(target=self.save, args=(item,), daemon=True).start()
        return {'ok': True}


def create_downloads():
    helper = SourceFileLoader('cage_download_copy', '/usr/local/bin/cage-save-download').load_module()
    with open('/etc/claude-cage/desktop-downloads.json') as config:
        workspace = json.load(config)['workspace']
    return Downloads(Path.home() / '.local/share/cage/desktop-downloads', workspace, helper.copy_to_workspace)
