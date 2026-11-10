import { mkdir, mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

// Historical reports are immutable inputs unless the caller selects an output.
export async function smokeCaptureDirectory(name) {
  if (process.env.CAGE_TEST_CAPTURE) {
    const directory = resolve(process.env.CAGE_TEST_CAPTURE);
    await mkdir(directory, { recursive: true });
    return directory;
  }
  return mkdtemp(join(tmpdir(), "cage-" + name + "-"));
}
