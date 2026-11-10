"""Build a QA-only Go runner with the Apple VM setup view bound to one temporary instance."""

import argparse
import json
import shutil
import subprocess
import tempfile
from pathlib import Path


SCRIPTS = Path(__file__).resolve().parent
REPO = SCRIPTS.parents[1]
VZ = "github.com/Code-Hex/vz/v3"


def run(*command):
    subprocess.run(command, check=True, cwd=REPO)


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise SystemExit(f"Upstream hook changed. Inspect the source containing {old!r}.")
    return text.replace(old, new)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("instance", type=Path, help="Directory of the disposable Lima macOS instance")
    parser.add_argument("output", type=Path, help="New directory for the signed QA runner and its library")
    parser.add_argument("--runner", default="./cmd/cage", help="Go main package which owns the fixture runtime")
    args = parser.parse_args()
    instance = args.instance.resolve(strict=True)
    temp_roots = {Path(tempfile.gettempdir()).resolve(), Path("/private/tmp").resolve()}
    if not instance.is_dir() or not any(root in instance.parents for root in temp_roots):
        parser.error("Select an instance inside a disposable temporary directory.")
    out = args.output.resolve()
    out.mkdir(mode=0o700)
    dylib = out / "libcagesetup.dylib"
    run("xcrun", "clang", "-target", "arm64-apple-macos14.0", "-fobjc-arc", "-Wall", "-Wextra", "-Werror",
        "-dynamiclib", SCRIPTS / "macos-setup-view.m", SCRIPTS / "macos-setup-bridge.m",
        "-framework", "AppKit", "-framework", "Virtualization", "-o", dylib)
    with tempfile.TemporaryDirectory(prefix="cage-setup-build-") as scratch:
        work = Path(scratch)
        module = json.loads(subprocess.check_output(["go", "list", "-m", "-json", VZ], cwd=REPO))
        if module.get("Version") != "v3.7.1":
            raise SystemExit("Review the native bridge for the current Code-Hex/vz version.")
        source = Path(module["Dir"])
        copy = work / "vz"
        shutil.copytree(source, copy)
        copy.chmod(0o700)
        shutil.copyfile(SCRIPTS / "macos-setup-bridge.h", copy / "cage_setup_bridge.h")
        (copy / dylib.name).symlink_to(dylib)
        binding = copy / "virtualization.go"
        text = replace_once(binding.read_text(), '# include "virtualization_15.h"',
                            '# include "virtualization_15.h"\n# include "cage_setup_bridge.h"')
        text = replace_once(text, "#cgo darwin LDFLAGS: -lobjc",
                            "#cgo darwin LDFLAGS: -L${SRCDIR} -lcagesetup -lobjc")
        binding.chmod(0o600)
        binding.write_text(replace_once(text, "C.startVirtualMachineWindow(", "C.CageRunSetupBridge("))

        driver = REPO / "third_party/lima/pkg/driver/vz/vz_driver_darwin.go"
        contents = driver.read_text()
        start = contents.index("func (l *LimaVzDriver) canRunGUI() bool {")
        end = contents.index("\n}", start)
        selected = json.dumps(str(instance), ensure_ascii=False)
        hook = ("func (l *LimaVzDriver) canRunGUI() bool {\n"
                f"\treturn l.Instance.Dir == {selected} && *l.Instance.Config.OS == limatype.DARWIN "
                '&& *l.Instance.Config.Video.Display == "offscreen"')
        modified = work / "driver.go"
        modified.write_text(contents[:start] + hook + contents[end:])
        runner = json.loads(subprocess.check_output(["go", "list", "-json", args.runner], cwd=REPO))
        runner_dir = Path(runner["Dir"]).resolve()
        if runner["Name"] != "main" or REPO not in runner_dir.parents:
            parser.error("Choose a main package in this checkout.")
        startup = work / "setup_thread.go"
        startup.write_text('package main\nimport "runtime"\nfunc init() { runtime.LockOSThread() }\n')
        overlay = work / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {
            str(driver): str(modified), str(runner_dir / "cage_setup_qa_thread_darwin.go"): str(startup),
        }}))
        mod = work / "qa.mod"
        shutil.copyfile(REPO / "go.mod", mod)
        shutil.copyfile(REPO / "go.sum", work / "qa.sum")
        run("go", "mod", "edit", "-modfile", mod, "-replace", f"{VZ}={copy}")
        run("go", "build", "-modfile", mod, "-overlay", overlay, "-o", out / "qa-runner", args.runner)
        run("codesign", "-f", "-s", "-", "--entitlements", REPO / "third_party/lima/vz.entitlements", out / "qa-runner")


if __name__ == "__main__":
    main()
