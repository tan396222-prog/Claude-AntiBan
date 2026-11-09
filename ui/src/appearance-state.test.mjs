import test from "node:test";
import assert from "node:assert/strict";
import {
  defaultAppearance,
  normalizeAppearance,
  themeVariables,
  terminalTheme,
  themeDocument,
  parseThemeDocument,
  sameTheme,
  colorGroups,
  terminalColorGroups,
} from "./appearance-state.mjs";

test("element overrides stay independent, reset returns to derived colors, and terminal follows live values", () => {
  const appearance = normalizeAppearance(structuredClone(defaultAppearance));
  const theme = appearance.lightTheme;
  const before = themeVariables(theme, "light");
  theme.colors = {
    chrome: "#123456",
    button: "#654321",
    buttonInk: "#abcdef",
    selection: "#aabbcc",
  };
  theme.terminal = { background: "#112233", red: "#fedcba", cursor: "#445566" };
  theme.surface = "#fafafa";
  const vars = themeVariables(theme, "light");
  assert.equal(vars["--chrome"], "#123456");
  assert.equal(vars["--button-ink"], "#abcdef");
  assert.equal(vars["--surface"], "#fafafa");
  assert.equal(vars["--border"], vars["--line"]);
  assert.notEqual(vars["--line"], before["--line"]);
  const terminal = terminalTheme(theme, "light");
  assert.equal(terminal.background, "#112233");
  assert.equal(terminal.red, "#fedcba");
  assert.equal(terminal.selectionBackground, "#aabbcc");
  delete theme.colors.chrome;
  assert.notEqual(themeVariables(theme, "light")["--chrome"], "#123456");
});

test("theme document round trips both variants and all editable colors, without device preferences or library", () => {
  const appearance = normalizeAppearance(structuredClone(defaultAppearance));
  appearance.lightTheme.colors = Object.fromEntries(
    colorGroups.flatMap((group) =>
      group.entries.map(([key]) => [key, "#123456"]),
    ),
  );
  appearance.darkTheme.terminal = Object.fromEntries(
    terminalColorGroups.flatMap((group) =>
      group.entries.map(([key]) => [key, "#abcdef"]),
    ),
  );
  appearance.darkTheme.fonts.code = "Menlo";
  const document = themeDocument("  自定义主题  ", appearance);
  const imported = parseThemeDocument(JSON.stringify(document));
  assert.equal(imported.name, "自定义主题");
  assert.ok(sameTheme(appearance, imported));
  assert.deepEqual(Object.keys(imported).sort(), [
    "darkTheme",
    "format",
    "lightTheme",
    "name",
    "version",
  ]);
  const copy = structuredClone(imported);
  copy.lightTheme.colors = Object.fromEntries(
    Object.entries(copy.lightTheme.colors).reverse(),
  );
  assert.ok(sameTheme(copy, imported));
  copy.darkTheme.terminal.red = "#000000";
  assert.equal(sameTheme(copy, imported), false);
});

test("invalid theme files are rejected before any mutation", () => {
  const valid = themeDocument("主题", defaultAppearance);
  const reject = (mutate) => {
    const data = structuredClone(valid);
    mutate(data);
    assert.throws(() => parseThemeDocument(JSON.stringify(data)));
  };
  for (const input of [
    "not json",
    "null",
    "[]",
    "{}",
    " ".repeat(256 * 1024 + 1),
  ])
    assert.throws(() => parseThemeDocument(input));
  reject((data) => {
    data.version = 2;
  });
  reject((data) => {
    data.name = "  ";
  });
  reject((data) => {
    delete data.darkTheme;
  });
  reject((data) => {
    data.lightTheme.accent = "#abc";
  });
  reject((data) => {
    data.lightTheme.colors = { chrome: "red" };
  });
  reject((data) => {
    data.lightTheme.colors = { unknown: "#abcdef" };
  });
  reject((data) => {
    data.lightTheme.terminal = { foreground: "url(https://example.com)" };
  });
  reject((data) => {
    data.lightTheme.fonts.code = "Menlo; color:red";
  });
  reject((data) => {
    data.lightTheme.contrast = 10.5;
  });
  reject((data) => {
    data.mode = "dark";
  });
  assert.equal(defaultAppearance.lightTheme.accent, "#339cff");
});

test("all built-in palettes produce valid, independently editable theme documents", async () => {
  const { themeCatalog, presets, presetNames, presetGroups, applyThemePreset } =
    await import("./appearance-presets.mjs");
  assert.equal(
    new Set(themeCatalog.map((item) => item.id)).size,
    themeCatalog.length,
  );
  assert.ok(Object.keys(presets.light).length >= 10);
  assert.ok(Object.keys(presets.dark).length >= 12);
  for (const item of themeCatalog) {
    assert.ok(presetGroups.includes(item.group));
    assert.equal(presetNames[item.id], item.name);
    for (const variant of ["light", "dark"]) {
      if (!item[variant]) continue;
      const current = structuredClone(defaultAppearance[variant + "Theme"]);
      current.colors = { danger: "#123456", toolbar: "#123456" };
      current.terminal = { foreground: "#123456", brightRed: "#123456" };
      current.fonts.code = "Menlo";
      const applied = applyThemePreset(current, variant, item.id);
      const appearance = normalizeAppearance({ [variant + "Theme"]: applied });
      const imported = parseThemeDocument(
        JSON.stringify(themeDocument(item.name, appearance)),
      );
      assert.ok(sameTheme(appearance, imported), item.id);
      assert.equal(applied.fonts.code, "Menlo");
      assert.notEqual(applied.colors.danger, "#123456");
      assert.notEqual(applied.terminal.foreground, "#123456");
      assert.ok(
        Object.values(themeVariables(applied, variant)).every(
          (value) => !String(value).includes("NaN"),
        ),
      );
      applied.colors.line = "#123456";
      applied.terminal.red = "#123456";
      assert.notEqual(presets[variant][item.id].colors.line, "#123456");
      assert.notEqual(presets[variant][item.id].terminal.red, "#123456");
    }
  }
});

function contrastRatio(left, right) {
  const luminance = (color) => {
    const [r, g, b] = [1, 3, 5]
      .map((index) => parseInt(color.slice(index, index + 2), 16) / 255)
      .map((value) =>
        value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
      );
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const a = luminance(left),
    b = luminance(right);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

test("accessible presets keep text, state colors, focus, borders and all ANSI text legible", async () => {
  const { applyThemePreset } = await import("./appearance-presets.mjs");
  for (const id of ["color-vision", "high-contrast"])
    for (const variant of ["light", "dark"]) {
      const theme = applyThemePreset(
        defaultAppearance[variant + "Theme"],
        variant,
        id,
      );
      const vars = themeVariables(theme, variant),
        terminal = terminalTheme(theme, variant);
      const threshold = id === "high-contrast" ? 7 : 4.5;
      const check = (foreground, background, minimum, name) =>
        assert.ok(
          contrastRatio(foreground, background) >= minimum,
          `${id}/${variant}/${name}: ${contrastRatio(foreground, background).toFixed(3)} < ${minimum}`,
        );
      for (const key of [
        "--ink",
        "--strong",
        "--muted",
        "--link",
        "--success",
        "--danger",
        "--warning",
        "--inactive",
      ]) {
        for (const background of [
          "--paper",
          "--chrome",
          "--chrome-dark",
          "--hover",
          "--selected",
          "--input",
        ]) {
          // State messages and links are shown on paper/panel surfaces, not selection rows.
          if (
            ["--hover", "--selected"].includes(background) &&
            !["--ink", "--strong"].includes(key)
          )
            continue;
          check(
            vars[key],
            vars[background],
            threshold,
            `${key} on ${background}`,
          );
        }
      }
      check(vars["--sidebar-ink"], vars["--sidebar"], threshold, "sidebar");
      check(vars["--toolbar-ink"], vars["--toolbar"], threshold, "toolbar");
      check(vars["--button-ink"], vars["--button"], threshold, "button");
      for (const background of ["--paper", "--chrome", "--input"]) {
        check(vars["--line"], vars[background], 3, "border");
        check(vars["--focus"], vars[background], 3, "focus");
      }
      for (const group of terminalColorGroups)
        for (const [key] of group.entries) {
          if (
            ["background", "selectionBackground", "cursorAccent"].includes(key)
          )
            continue;
          check(
            terminal[key],
            terminal.background,
            threshold,
            `terminal ${key}`,
          );
        }
      assert.equal(theme.opaqueWindows, true);
      assert.notEqual(vars["--success"], vars["--danger"]);
      check(
        terminal.foreground,
        terminal.selectionBackground,
        threshold,
        "terminal selection",
      );
      check(terminal.cursorAccent, terminal.cursor, threshold, "cursor text");
    }
});
