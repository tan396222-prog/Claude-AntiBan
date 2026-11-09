"""Prefetch only the Chromium archives selected by the upcoming APT transaction."""

import os
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlsplit

# Older or minimal container images can still use their normal apt-get install.
try:
    import apt
    import apt_pkg
    from apt.progress.base import AcquireProgress
except ImportError:
    print("Using configured APT sources (python3-apt unavailable).")
    sys.exit(0)


MIRRORS = ("https://mirrors.nju.edu.cn/debian-security/", "https://mirrors.ustc.edu.cn/debian-security/")
SECURITY_LIST = Path("/etc/apt/mirrors/debian-security.list")
OFFICIAL = ("https://deb.debian.org/debian-security/", "https://security.debian.org/debian-security/")


def stock_security_uri(uri):
    # The Debian cloud image delegates this URI to a separate security list.
    # A customized list or other repository keeps its own download routes.
    if uri.startswith("mirror+file:" + str(SECURITY_LIST) + "/"):
        if not SECURITY_LIST.is_file():
            return False
        return SECURITY_LIST.read_text().strip().rstrip("/") + "/" in OFFICIAL
    site = urlsplit(uri)
    return (site.scheme in ("http", "https") and site.netloc in ("deb.debian.org", "security.debian.org")
            and site.path.startswith("/debian-security/"))


class Progress(AcquireProgress):
    def __init__(self, package, size):
        super().__init__()
        self.package = package
        self.size = size
        self.previous = -1

    def report(self, received):
        if os.environ.get("CAGE_SETUP_PROGRESS") == "1" and received != self.previous:
            print(f"CAGE_TOOL_BYTES {self.package}.deb {received} {self.size}", flush=True)
            self.previous = received

    def pulse(self, owner):
        self.report(min(int(self.current_bytes), self.size))
        return True


def fetch(package, version, target):
    hashes = apt_pkg.HashStringList()
    hashes.append(apt_pkg.HashString("SHA256", version.sha256))
    partial = target.parent / "partial" / (target.name + ".cage")
    for mirror in MIRRORS:
        print(f"Downloading {package} {version.version} from {mirror}", flush=True)
        progress = Progress(package, version.size)
        acquire = apt_pkg.Acquire(progress)
        item = apt_pkg.AcquireFile(acquire, mirror + version.filename, hashes, version.size, destfile=str(partial))
        acquire.run()
        if item.complete and item.status == item.STAT_DONE and hashes.verify_file(str(partial)):
            # APT has verified the official digest. Publish on this volume
            # under its own archive lock; never expose an unverified .deb.
            partial.chmod(0o644)
            partial.replace(target)
            progress.report(version.size)
            return
        if item.status == item.STAT_AUTH_ERROR or item.complete:
            partial.unlink(missing_ok=True)
        # APT quarantines responses with an incorrect digest under .FAILED.
        Path(str(partial) + ".FAILED").unlink(missing_ok=True)
        print(f"Mirror could not supply the authenticated {package} archive.", flush=True)
    print(f"Using the official Debian security source for {package}.", flush=True)


def prefetch(install_options):
    # --print-uris resolves the same install/options without installing. Its
    # destination names include APT's architecture and any escaped epoch.
    plan = subprocess.check_output(
        ["apt-get", "--print-uris", "install", "-y", *install_options], text=True
    )
    cache = apt.Cache()
    archives = Path(apt_pkg.config.find_dir("Dir::Cache::archives"))
    apt_pkg.config.set("Acquire::Retries", "0")
    apt_pkg.config.set("Acquire::https::Timeout", "60")
    with apt_pkg.FileLock(str(archives / "lock")):
        for line in plan.splitlines():
            if not line.startswith("'"):
                continue
            uri, filename, *_ = line.split()
            package = filename.partition("_")[0]
            if package not in ("chromium", "chromium-common") or Path(filename).name != filename:
                continue
            candidate = cache[package].candidate
            if not candidate or candidate.uri != uri.strip("'") or not stock_security_uri(candidate.uri):
                continue
            origin = candidate.origins[0]
            if not (origin.trusted and origin.origin == "Debian" and origin.label == "Debian-Security" and candidate.sha256):
                continue
            fetch(package, candidate, archives / filename)


if __name__ == "__main__":
    try:
        prefetch(sys.argv[1:])
    except (OSError, apt_pkg.Error, subprocess.CalledProcessError) as error:
        # apt-get owns dependency resolution, the installation, and its error
        # messages, including in the presence of concurrent package operations.
        print(f"Using configured APT sources ({type(error).__name__}).", flush=True)
