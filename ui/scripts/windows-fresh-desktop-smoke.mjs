import { pathToFileURL } from "node:url";
const { chromium } = await import(
  process.env.CAGE_WINDOWS_QA_PLAYWRIGHT
    ? pathToFileURL(process.env.CAGE_WINDOWS_QA_PLAYWRIGHT).href
    : "playwright"
);
import path from "node:path";
let input = "";
for await (const chunk of process.stdin) input += chunk;
const config = JSON.parse(input);
const browser = await chromium.launch({
  executablePath: path.join(
    process.env["ProgramFiles(x86)"],
    "Microsoft",
    "Edge",
    "Application",
    "msedge.exe",
  ),
  headless: true,
});
try {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
  });
  await ctx.addCookies(
    config.cookies.map((c) => ({
      ...c,
      url: config.url,
      httpOnly: true,
      sameSite: "Strict",
    })),
  );
  const page = await ctx.newPage();
  page.setDefaultTimeout(30000);
  const pointers = [];
  page.on("websocket", (socket) => {
    if (new URL(socket.url()).pathname !== "/api/claude-desktop/vnc") return;
    socket.on("framesent", ({ payload }) => {
      if (!Buffer.isBuffer(payload) || payload.length !== 6 || payload[0] !== 5)
        return;
      if (pointers.length < 24)
        pointers.push([
          payload[1],
          payload.readUInt16BE(2),
          payload.readUInt16BE(4),
        ]);
    });
  });
  await page.goto(config.url);
  const nav = page.getByRole("navigation", { name: "主导航" });
  await nav
    .getByRole("button", { name: "Claude Desktop", exact: true })
    .click();
  const canvas = page
    .locator('section[aria-label="Claude Desktop"] canvas:visible')
    .last();
  await canvas.waitFor({ timeout: 45000 });
  // The guest view is an RFB canvas. Match the two observed public screens of
  // the pinned Windows app before issuing a click or accepting the result.
  function onboardingScreen(requireSignIn) {
    const el = Array.from(
      document.querySelectorAll('section[aria-label="Claude Desktop"] canvas'),
    ).find(
      (c) =>
        c.getBoundingClientRect().width > 100 &&
        c.width >= 1100 &&
        c.height >= 240,
    );
    if (!el) return;
    const { width, height } = el;
    const pixels = el.getContext("2d").getImageData(0, 0, width, height).data;
    const near = (x, y, red, green, blue, tolerance) => {
      const index = (y * width + x) * 4;
      return (
        Math.abs(pixels[index] - red) <= tolerance &&
        Math.abs(pixels[index + 1] - green) <= tolerance &&
        Math.abs(pixels[index + 2] - blue) <= tolerance
      );
    };
    const cx = Math.floor(width / 2);
    let logo = 0,
      googleBlue = 0,
      googleGreen = 0,
      googleRed = 0;
    for (let y = Math.floor(height * 0.1); y < height * 0.8; y += 2) {
      for (
        let x = Math.max(0, cx - 220);
        x < Math.min(width, cx + 220);
        x += 2
      ) {
        if (near(x, y, 220, 115, 77, 22)) logo++;
        if (near(x, y, 66, 133, 244, 24)) googleBlue++;
        if (near(x, y, 52, 168, 83, 24)) googleGreen++;
        if (near(x, y, 234, 67, 53, 24)) googleRed++;
      }
    }
    const signIn =
      googleBlue >= 1 && googleRed >= 1 && googleGreen >= 1 && logo < 80;
    if (!signIn && (requireSignIn || logo < 100)) return;
    const lower = signIn ? 0.33 : 0.6,
      upper = signIn ? 0.83 : 0.999;
    let first = -1,
      last = -1;
    for (let y = Math.floor(height * lower); y < height * upper; y++) {
      if (
        !near(cx - 45, y, 24, 24, 24, 15) ||
        !near(cx + 45, y, 24, 24, 24, 15)
      )
        continue;
      let left = cx - 45,
        right = cx + 45;
      while (left > 0 && near(left, y, 24, 24, 24, 15)) left--;
      while (right < width - 1 && near(right, y, 24, 24, 24, 15)) right++;
      if (
        right - left < 230 ||
        right - left > 820 ||
        Math.abs((right + left) / 2 - cx) > 6
      )
        continue;
      if (first < 0) first = y;
      last = y;
    }
    if (first < 0 || last - first < 20 || last - first > 100) return;
    const bounds = el.getBoundingClientRect();
    return {
      signIn,
      x: bounds.left + bounds.width / 2,
      y: bounds.top + bounds.height * ((first + last) / 2 / height),
    };
  }
  const screenshot = async (name) =>
    page.screenshot({
      clip: { x: 0, y: 0, width: 1440, height: 855 },
      path: path.join(config.evidence, name + ".png"),
    });
  const state = await (
    await page.waitForFunction(onboardingScreen, false, { timeout: 60000 })
  ).jsonValue();
  if (!state.signIn) {
    await screenshot("windows-workbench-welcome");
    // Wait for the noVNC resize acknowledgement. The very first WGC frame
    // is the app's 600×600 startup window and cannot locate a later button.
    await page.waitForTimeout(1000);
    const live = await page.evaluate(onboardingScreen, false);
    if (!live || live.signIn)
      throw new Error("Welcome button changed before clicking");
    await page.mouse.click(live.x, live.y);
    try {
      await page.waitForFunction(onboardingScreen, true, { timeout: 80000 });
    } catch {
      await screenshot("windows-workbench-after-click");
      throw new Error(
        "Login form was not observed; welcome point=" +
          JSON.stringify(live) +
          "; guest pointer coordinates=" +
          JSON.stringify(pointers),
      );
    }
  }
  await screenshot("windows-workbench-signin");
  console.log(
    "Verified the official Google/email form inside the shared Windows workbench.",
  );
} finally {
  await browser.close();
}
