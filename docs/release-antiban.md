# 发布新版本（Claude 防封维护者）

已安装 v0.1.7-rc.1.antiban.2 及以上版本的用户，会在 Cage 中自动收到新版本弹窗，点「立即更新」即可完成更新。发布步骤如下：

1. 在 x86_64 的 Debian 13 / Ubuntu 24.04+ 上准备好 Go、Node.js、`gh`（已登录，具备 `repo` 权限），以及
   `sudo apt install pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev libjson-glib-dev dpkg-dev`。
2. 改好代码并提交到 `main`。
3. 写一份中文更新说明，例如 `notes.md`。
4. 运行：

   ```bash
   scripts/release-antiban.sh 0.1.7-rc.1.antiban.4 notes.md
   ```

   脚本会修改版本号、打包 `.deb`、生成 `SHA256SUMS`、提交并推送标签，然后创建 GitHub Release，并上传 `Cage-<版本>-linux-amd64.deb`、`SHA256SUMS` 和 mihomo 源码包。

注意：

- 版本号必须比上一版大。可以把末尾数字加 1，例如 `antiban.3` → `antiban.4`；也可以改成 `0.1.8` 之类的新版本号。预发布部分里不要使用 `-`。
- Release 必须是已发布的状态，不能是草稿，并且必须包含 `.deb` 和 `SHA256SUMS`。文件名要保持 `Cage-<版本>-linux-amd64.deb`，否则应用找不到更新。
- 应用通过 Cage 当前选择的代理访问 GitHub 检查更新：打开时检查一次，之后每 6 小时检查一次；代理未运行时每 5 分钟重试。用户也可以在菜单「设置 → 应用更新…」中手动检查。
