import { execFileSync } from "node:child_process";

const localFiles = new Set([
  "AGENTS.md", "CONTRIBUTING.md", "RELEASING.md", "CHANGELOG.md",
  "release.sh", "tests/release.sh", "flake.nix", "flake.lock", "packaging/nfpm.yaml",
  "docs/BACKLOG.md", "docs/agent-vm-upstream.md", "docs/ci-cd.md",
  "docs/public-releases.md", "docs/releasing.md", "patches/README.md",
  "third_party/SOURCES", "scripts/third-party-sync",
  "scripts/guest-os/macos-setup-view.md", "tools/tailnet/README.md",
  "www/README.md", "internal/exitpolicy/data/README.md",
  "ui/vendor/novnc/README.cage.md", "ui/vendor/novnc/vendor/pako/README.md",
]);
const localDirectories = [
  "docs/design/", "docs/research/", "docs/plans/", "docs/test-reports/",
  "promo/", "ui/prototypes/", ".oil-ui/",
];
const tracked = execFileSync("git", ["ls-files", "-z"], { encoding: "utf8" })
  .split("\0").filter(Boolean);
const rejected = tracked.filter(file =>
  localFiles.has(file) || localDirectories.some(prefix => file.startsWith(prefix)) ||
  file.startsWith("third_party/") && (
    /\.(?:md|rst|adoc)$/.test(file) || file.includes("/.github/") ||
    /\/\.(?:gitignore|gitattributes)$/.test(file)
  ),
);
if (rejected.length) {
  throw new Error(`Local development material must be ignored, not tracked:\n${rejected.join("\n")}`);
}
if (process.argv.includes("--untracked")) {
  const pending = execFileSync("git", ["ls-files", "--others", "--exclude-standard"], { encoding: "utf8" }).trim();
  if (pending) throw new Error(`Classify new files before completing the task:\n${pending}`);
}
console.log(`Verified repository scope for ${tracked.length} tracked files.`);
