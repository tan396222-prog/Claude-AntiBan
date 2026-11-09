#!/usr/bin/python3
"""Publish one completed browser download into the fixed shared workspace."""
import errno
import json
import os
import pwd
import re
import secrets
import stat
import sys


def checked_download(source_name, name):
    if not re.fullmatch(r'[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}', source_name):
        raise ValueError('下载标识无效')
    if not name or name != os.path.basename(name) or name in ('.', '..') or '\0' in name:
        raise ValueError('下载文件名无效')


def copy_to_workspace(source, size, workspace, name):
    """Run as the coding user; final names only become visible after fsync."""
    parent = os.open(workspace, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    destination = None
    published = None
    temporary = '.cage-download-' + secrets.token_hex(12) + '.part'
    try:
        try:
            os.mkdir('Downloads', mode=0o755, dir_fd=parent)
        except FileExistsError:
            pass
        destination = os.open('Downloads', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
        output = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=destination)
        with os.fdopen(output, 'wb') as dst:
            remaining = size
            while remaining:
                chunk = os.read(source, min(remaining, 1024 * 1024))
                if not chunk:
                    raise OSError(errno.EIO, '下载源文件在保存过程中发生变化')
                dst.write(chunk)
                remaining -= len(chunk)
            dst.flush()
            os.fchmod(dst.fileno(), 0o644)
            os.fsync(dst.fileno())
            root, extension = os.path.splitext(name)
            for number in range(1000):
                candidate = name if number == 0 else f'{root} ({number}){extension}'
                try:
                    # The complete file is published without replacing any entry.
                    os.link(temporary, candidate, src_dir_fd=destination, dst_dir_fd=destination, follow_symlinks=False)
                    published = {'ok': True, 'path': os.path.join(workspace, 'Downloads', candidate), 'name': candidate, 'bytes': size}
                    return published
                except FileExistsError:
                    continue
            raise OSError(errno.EEXIST, '同名文件过多，请整理 Downloads 后重试')
    finally:
        if destination is not None:
            try:
                os.unlink(temporary, dir_fd=destination)
            except FileNotFoundError:
                pass
            except OSError:
                if published is None:
                    raise
                published['warning'] = '文件已保存到本机；共享目录中的临时文件暂时无法清理。'
            finally:
                os.close(destination)
        os.close(parent)


def remove_source_if_unchanged(directory, name, expected):
    """Do not remove a replacement created after the source was opened."""
    try:
        current = os.stat(name, dir_fd=directory, follow_symlinks=False)
    except FileNotFoundError:
        return True
    identity = lambda st: (st.st_dev, st.st_ino, st.st_uid, st.st_size, st.st_mtime_ns)
    if not stat.S_ISREG(current.st_mode) or identity(current) != identity(expected):
        return False
    os.unlink(name, dir_fd=directory)
    return True


def error_message(error):
    if isinstance(error, ValueError):
        return str(error)
    if isinstance(error, OSError):
        if error.errno == errno.ENOSPC:
            return '空间不足，文件尚未保存到本机。清理空间后可重试。'
        if error.errno in (errno.EACCES, errno.EPERM, errno.EROFS):
            return '无法写入共享下载目录。检查目录权限和共享连接后可重试。'
        if error.errno in (errno.ELOOP, errno.ENOTDIR):
            return '下载目录包含符号链接或已不再是目录，请修复后重试。'
        if error.errno == errno.ENOENT:
            return '下载源文件或共享目录不可用，请检查后重试。'
    return '文件尚未保存到本机。检查共享连接和可用空间后可重试。'


def main():
    source_name, name = sys.argv[1:] if len(sys.argv) == 3 else ('', '')
    checked_download(source_name, name)
    with open('/etc/claude-cage/browser-control.json') as config:
        workspace = json.load(config)['workspace']
    directory = os.open('/home/cage-browser/Downloads', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    source = None
    try:
        browser_uid = pwd.getpwnam('cage-browser').pw_uid
        if os.fstat(directory).st_uid != browser_uid:
            raise ValueError('下载目录所有者无效')
        source = os.open(source_name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
        source_stat = os.fstat(source)
        if not stat.S_ISREG(source_stat.st_mode) or source_stat.st_uid != browser_uid:
            raise ValueError('下载源文件不是普通文件')
        user = pwd.getpwnam('cage')
        receive, send = os.pipe()
        child = os.fork()
        if child == 0:
            os.close(receive)
            try:
                # Only the parent retains permission to clean up browser files.
                os.setgroups([user.pw_gid])
                os.setgid(user.pw_gid)
                os.setuid(user.pw_uid)
                result = copy_to_workspace(source, source_stat.st_size, workspace, name)
            except Exception as error:
                result = {'ok': False, 'error': error_message(error)}
            with os.fdopen(send, 'wb') as pipe:
                pipe.write(json.dumps(result, ensure_ascii=False).encode())
            os._exit(0 if result['ok'] else 1)
        os.close(send)
        with os.fdopen(receive, 'rb') as pipe:
            data = pipe.read(16385)
        _, status = os.waitpid(child, 0)
        if len(data) > 16384:
            raise ValueError('下载保存结果无效')
        result = json.loads(data)
        if os.waitstatus_to_exitcode(status) != 0 or not result.get('ok'):
            print(json.dumps({'ok': False, 'error': result.get('error', '下载保存未完成')}, ensure_ascii=False))
            return 1
        try:
            cleaned = remove_source_if_unchanged(directory, source_name, source_stat)
        except OSError:
            cleaned = False
        if not cleaned:
            result['warning'] = '文件已保存到本机；客机中的下载源文件发生变化或暂时无法清理。'
        print(json.dumps(result, ensure_ascii=False))
        return 0
    finally:
        if source is not None:
            os.close(source)
        os.close(directory)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as error:
        print(json.dumps({'ok': False, 'error': error_message(error)}, ensure_ascii=False))
        sys.exit(1)
