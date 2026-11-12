/* In-app updates for the Linux .deb (Claude 防封 fork).
 * The backend (`cage app-update check|download`) finds and downloads the
 * release through Cage's selected proxy and verifies its SHA-256. This shell
 * re-verifies the package identity, stops the environment, and hands the
 * package to a detached helper that installs it with administrator rights
 * after every Cage process has exited, then reopens Cage. */
static char *update_json, *update_phase, *update_latest, *update_archive, *update_sha, *update_prompted;
static gboolean update_busy, update_menu_check;
static GSubprocess *update_process;
static void update_install(void);

static char *update_dir(void) {
    char *profile = g_path_get_dirname(app.profile);
    char *dir = g_build_filename(profile, "updates", NULL);
    g_free(profile);
    return dir;
}
static char *update_result_path(void) {
    char *dir = update_dir();
    char *path = g_build_filename(dir, "linux-update-result.json", NULL);
    g_free(dir);
    return path;
}
static void update_publish(void) {
    if (!update_json) return;
    char *code = g_strdup_printf("window.dispatchEvent(new CustomEvent('cage-app-update',{detail:%s}))", update_json);
    script(code);
    g_free(code);
}
/* Takes ownership of nothing; copies what it keeps. */
static void update_store(JsonObject *object) {
    const char *phase = text_member(object, "phase");
    if (*phase) { g_free(update_phase); update_phase = g_strdup(phase); }
    const char *latest = text_member(object, "latestVersion");
    if (*latest) { g_free(update_latest); update_latest = g_strdup(latest); }
    const char *archive = text_member(object, "archivePath");
    if (*archive) { g_free(update_archive); update_archive = g_strdup(archive); }
    const char *sha = text_member(object, "sha256");
    if (*sha) { g_free(update_sha); update_sha = g_strdup(sha); }
    /* Private paths stay in the native shell. */
    if (json_object_has_member(object, "archivePath")) json_object_remove_member(object, "archivePath");
    if (json_object_has_member(object, "sha256")) json_object_remove_member(object, "sha256");
    json_object_set_string_member(object, "currentVersion", CAGE_VERSION);
    if (update_latest && !json_object_has_member(object, "latestVersion")) json_object_set_string_member(object, "latestVersion", update_latest);
    if (!json_object_has_member(object, "retryAt")) json_object_set_string_member(object, "retryAt", "");
    JsonNode *node = json_node_new(JSON_NODE_OBJECT);
    json_node_set_object(node, object);
    g_free(update_json);
    update_json = json_to_string(node, FALSE);
    json_node_unref(node);
    update_publish();
}
static void update_set(const char *phase, const char *message, gboolean retryable) {
    JsonObject *object = json_object_new();
    json_object_set_string_member(object, "phase", phase);
    if (message) json_object_set_string_member(object, "message", message);
    if (retryable) json_object_set_boolean_member(object, "retryable", TRUE);
    update_store(object);
    json_object_unref(object);
}
static void update_dialog(GtkMessageType type, const char *text) {
    GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(app.window), GTK_DIALOG_DESTROY_WITH_PARENT, type, GTK_BUTTONS_CLOSE, "%s", text);
    gtk_window_set_title(GTK_WINDOW(dialog), "Cage 应用更新");
    g_signal_connect(dialog, "response", G_CALLBACK(gtk_widget_destroy), NULL);
    gtk_widget_show(dialog);
}
static void update_prompt_response(GtkDialog *dialog, gint response, gpointer data) {
    (void)data;
    gtk_widget_destroy(GTK_WIDGET(dialog));
    if (response == GTK_RESPONSE_ACCEPT) update_install();
}
/* The popup: shown once per new version per launch, and on demand from the menu. */
static void update_prompt(void) {
    if (!update_latest) return;
    GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(app.window), GTK_DIALOG_DESTROY_WITH_PARENT, GTK_MESSAGE_INFO, GTK_BUTTONS_NONE,
        "发现新版本 %s", update_latest);
    gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog),
        "当前版本：%s\n\n点击“立即更新”后，Cage 会下载并校验新版本，停止环境（终端任务会结束，项目和登录状态会保留），安装完成后自动重新打开。安装时可能需要输入一次电脑的管理员密码。\n\n请先保存正在编辑的文件。",
        CAGE_VERSION);
    gtk_dialog_add_buttons(GTK_DIALOG(dialog), "稍后", GTK_RESPONSE_CANCEL, "立即更新", GTK_RESPONSE_ACCEPT, NULL);
    gtk_dialog_set_default_response(GTK_DIALOG(dialog), GTK_RESPONSE_ACCEPT);
    gtk_window_set_title(GTK_WINDOW(dialog), "Cage 应用更新");
    g_signal_connect(dialog, "response", G_CALLBACK(update_prompt_response), NULL);
    gtk_widget_show(dialog);
    gtk_window_present(GTK_WINDOW(dialog));
}

typedef void (*UpdateDone)(gboolean success, gpointer data);
typedef struct { GSubprocess *process; GDataInputStream *input; UpdateDone done; gpointer data; } UpdateRun;
static void update_run_waited(GObject *source, GAsyncResult *result, gpointer data) {
    UpdateRun *run = data;
    gboolean success = g_subprocess_wait_check_finish(G_SUBPROCESS(source), result, NULL);
    if (update_process == run->process) update_process = NULL;
    run->done(success, run->data);
    g_object_unref(run->input); g_object_unref(run->process); g_free(run);
}
static void update_run_line(GObject *source, GAsyncResult *result, gpointer data) {
    UpdateRun *run = data;
    char *line = g_data_input_stream_read_line_finish_utf8(G_DATA_INPUT_STREAM(source), result, NULL, NULL);
    if (!line) { g_subprocess_wait_async(run->process, NULL, update_run_waited, run); return; }
    JsonParser *parser = json_parser_new();
    if (strlen(line) < 65536 && json_parser_load_from_data(parser, line, -1, NULL) && JSON_NODE_HOLDS_OBJECT(json_parser_get_root(parser)))
        update_store(json_node_get_object(json_parser_get_root(parser)));
    g_object_unref(parser); g_free(line);
    g_data_input_stream_read_line_async(run->input, G_PRIORITY_DEFAULT, NULL, update_run_line, run);
}
static gboolean update_run(const char *operation, UpdateDone done, gpointer data) {
    GError *error = NULL;
    GSubprocessLauncher *launcher = g_subprocess_launcher_new(G_SUBPROCESS_FLAGS_STDOUT_PIPE | G_SUBPROCESS_FLAGS_STDERR_SILENCE);
    g_subprocess_launcher_setenv(launcher, "CLAUDE_CAGE_CONFIG", app.profile, TRUE);
    const char *args[] = {app.backend, "app-update", operation, NULL};
    GSubprocess *process = g_subprocess_launcher_spawnv(launcher, args, &error);
    g_object_unref(launcher);
    if (!process) { g_clear_error(&error); return FALSE; }
    UpdateRun *run = g_new0(UpdateRun, 1);
    run->process = process; run->done = done; run->data = data;
    run->input = g_data_input_stream_new(g_subprocess_get_stdout_pipe(process));
    update_process = process;
    g_data_input_stream_read_line_async(run->input, G_PRIORITY_DEFAULT, NULL, update_run_line, run);
    return TRUE;
}

static void update_checked(gboolean success, gpointer data) {
    PendingReply *pending = data;
    update_busy = FALSE;
    if (!success && g_strcmp0(update_phase, "failed") && g_strcmp0(update_phase, "waiting")) update_set("failed", "更新检查未完成，请稍后重试", TRUE);
    if (pending) { reply_json(pending->reply, pending->context, update_json); pending_free(pending); }
    gboolean menu = update_menu_check;
    update_menu_check = FALSE;
    if (!g_strcmp0(update_phase, "available") && update_latest) {
        if (menu || g_strcmp0(update_prompted, update_latest)) {
            g_free(update_prompted); update_prompted = g_strdup(update_latest);
            update_prompt();
        }
    } else if (menu) {
        if (!g_strcmp0(update_phase, "current")) {
            char *text = g_strdup_printf("已是最新版本（%s）。", CAGE_VERSION);
            update_dialog(GTK_MESSAGE_INFO, text); g_free(text);
        } else {
            JsonParser *parser = json_parser_new();
            const char *message = "";
            if (update_json && json_parser_load_from_data(parser, update_json, -1, NULL)) message = text_member(json_node_get_object(json_parser_get_root(parser)), "message");
            char *text = g_strdup_printf("暂时无法检查更新：%s", *message ? message : "请确认所选代理可以连接 GitHub 后重试。");
            update_dialog(GTK_MESSAGE_WARNING, text); g_free(text);
            g_object_unref(parser);
        }
    }
}
static void update_check(PendingReply *pending) {
    if (update_busy) {
        if (pending) { reply_json(pending->reply, pending->context, update_json); pending_free(pending); }
        return;
    }
    update_busy = TRUE;
    if (!update_run("check", update_checked, pending)) update_checked(FALSE, pending);
}

static gboolean update_file_sha256(const char *path, char **out) {
    FILE *file = fopen(path, "rb");
    if (!file) return FALSE;
    GChecksum *checksum = g_checksum_new(G_CHECKSUM_SHA256);
    guchar *buffer = g_malloc(1 << 20);
    size_t n;
    while ((n = fread(buffer, 1, 1 << 20, file)) > 0) g_checksum_update(checksum, buffer, n);
    gboolean ok = !ferror(file);
    fclose(file); g_free(buffer);
    if (ok) *out = g_strdup(g_checksum_get_string(checksum));
    g_checksum_free(checksum);
    return ok;
}
static char *update_deb_field(const char *path, const char *field) {
    const char *argv[] = {"dpkg-deb", "-f", path, field, NULL};
    char *output = NULL;
    gint status = 1;
    if (!g_spawn_sync(NULL, (char **)argv, NULL, G_SPAWN_SEARCH_PATH | G_SPAWN_STDERR_TO_DEV_NULL, NULL, NULL, &output, NULL, &status, NULL) || status != 0) {
        g_free(output); return NULL;
    }
    return g_strstrip(output);
}
static const char update_helper[] =
"#!/bin/sh\n"
"# Cage Linux update helper: installs one verified .deb after Cage exits.\n"
"deb=\"$1\"; parent=\"$2\"; result=\"$3\"; version=\"$4\"; expected=\"$5\"\n"
"exec >>\"${result%.json}.log\" 2>&1\n"
"echo \"$(date -Is) installing $version\"\n"
"relaunch() { if command -v setsid >/dev/null 2>&1; then setsid /usr/bin/cage-desktop </dev/null >/dev/null 2>&1 & else /usr/bin/cage-desktop </dev/null >/dev/null 2>&1 & fi; }\n"
"finish() { printf '{\"status\":\"%s\",\"version\":\"%s\",\"message\":\"%s\"}\\n' \"$1\" \"$version\" \"$2\" > \"$result.tmp\" && mv \"$result.tmp\" \"$result\"; relaunch; exit 0; }\n"
"i=0; while kill -0 \"$parent\" 2>/dev/null; do i=$((i+1)); [ $i -gt 600 ] && finish failed '等待 Cage 窗口退出超时，更新未安装'; sleep 0.2; done\n"
"i=0; while :; do busy=; for p in /proc/[0-9]*; do e=$(readlink \"$p/exe\" 2>/dev/null) || continue; case \"$e\" in /usr/lib/claude-cage/*) busy=1;; esac; done; [ -z \"$busy\" ] && break; i=$((i+1)); [ $i -gt 300 ] && finish failed 'Cage 后台仍在运行，更新未安装。请退出 Cage 后重试'; sleep 0.2; done\n"
"status=127\n"
"if sudo -n true 2>/dev/null; then sudo -n dpkg -i \"$deb\"; status=$?; fi\n"
"if [ $status -eq 127 ] && command -v pkexec >/dev/null 2>&1; then pkexec /usr/bin/dpkg -i \"$deb\"; status=$?; fi\n"
"if [ $status -eq 127 ] && command -v zenity >/dev/null 2>&1; then pw=$(zenity --password --title='Cage 更新需要管理员密码') && { printf '%s\\n' \"$pw\" | sudo -S -p '' dpkg -i \"$deb\"; status=$?; } || status=126; pw=; fi\n"
"if [ $status -eq 127 ] && command -v kdialog >/dev/null 2>&1; then pw=$(kdialog --password 'Cage 更新需要管理员密码') && { printf '%s\\n' \"$pw\" | sudo -S -p '' dpkg -i \"$deb\"; status=$?; } || status=126; pw=; fi\n"
"installed=$(dpkg-query -W -f='${Version}' claude-cage 2>/dev/null)\n"
"if [ \"$installed\" = \"$expected\" ]; then rm -rf \"$(dirname \"$deb\")\"; finish installed \"已更新到 $version\"; fi\n"
"if [ $status -eq 126 ]; then finish failed '未获得管理员授权，更新未安装'; fi\n"
"if [ $status -eq 127 ]; then finish failed '找不到可用的管理员授权方式，请手动安装更新包'; fi\n"
"finish failed '安装更新包失败，请查看更新日志后重试'\n";

static void update_child_setup(gpointer data) { (void)data; setsid(); }
static void update_prepared(JsonObject *object, const GError *error, gpointer data) {
    (void)data;
    if (error || g_strcmp0(text_member(object, "status"), "prepared")) {
        update_busy = FALSE;
        const char *message = error ? error->message : text_member(object, "message");
        update_set("failed", *message ? message : "无法停止环境，更新未安装", TRUE);
        return;
    }
    char *dir = update_dir();
    char *helper = g_build_filename(dir, "linux-install.sh", NULL);
    char *result = update_result_path();
    char *expected = g_strdup(update_latest);
    char *dash = strchr(expected, '-');
    if (dash) *dash = '~';
    char pid[32];
    g_snprintf(pid, sizeof(pid), "%ld", (long)getpid());
    GError *spawn_error = NULL;
    g_unlink(result);
    gboolean ok = g_file_set_contents(helper, update_helper, -1, &spawn_error) && g_chmod(helper, 0700) == 0;
    if (ok) {
        const char *argv[] = {"/bin/sh", helper, update_archive, pid, result, update_latest, expected, NULL};
        ok = g_spawn_async(NULL, (char **)argv, NULL, G_SPAWN_STDOUT_TO_DEV_NULL | G_SPAWN_STDERR_TO_DEV_NULL, update_child_setup, NULL, NULL, &spawn_error);
    }
    g_clear_error(&spawn_error);
    g_free(dir); g_free(helper); g_free(result); g_free(expected);
    if (!ok) { update_busy = FALSE; update_set("failed", "无法启动更新安装程序", TRUE); return; }
    g_application_quit(G_APPLICATION(app.application));
}
static void update_downloaded(gboolean success, gpointer data) {
    (void)data;
    if (!success || g_strcmp0(update_phase, "downloaded") || !update_archive || !update_sha || !update_latest) {
        update_busy = FALSE;
        if (g_strcmp0(update_phase, "failed") && g_strcmp0(update_phase, "waiting")) update_set("failed", "更新下载未完成，请重试", TRUE);
        return;
    }
    update_set("verifying", "正在验证更新", FALSE);
    char *digest = NULL, *package = update_deb_field(update_archive, "Package"), *version = update_deb_field(update_archive, "Version");
    char *expected = g_strdup(update_latest);
    char *dash = strchr(expected, '-');
    if (dash) *dash = '~';
    const char *problem = NULL;
    if (!update_file_sha256(update_archive, &digest) || g_ascii_strcasecmp(digest, update_sha)) problem = "更新包 SHA-256 校验失败，请重试";
    else if (g_strcmp0(package, "claude-cage") || g_strcmp0(version, expected)) problem = "更新包的软件名或版本与发布信息不一致";
    g_free(digest); g_free(package); g_free(version); g_free(expected);
    if (problem) { update_busy = FALSE; update_set("failed", problem, TRUE); return; }
    update_set("installing", "正在安装更新，完成后自动重启", FALSE);
    const char *args[] = {app.backend, "prepare-update", "--stop", "--json", NULL};
    backend_arguments(args, 300, update_prepared, NULL);
}
static void update_install(void) {
    if (update_busy) return;
    update_busy = TRUE;
    update_set("checking", "正在准备更新", FALSE);
    if (!update_run("download", update_downloaded, NULL)) update_downloaded(FALSE, NULL);
}
static void update_cancel(void) {
    if (update_process && !g_strcmp0(update_phase, "downloading")) g_subprocess_send_signal(update_process, SIGTERM);
}
/* The first status after a relaunch reports the helper's result once. */
static void update_status(void) {
    if (update_json) return;
    char *path = update_result_path(), *contents = NULL;
    JsonParser *parser = json_parser_new();
    if (g_file_get_contents(path, &contents, NULL, NULL) && json_parser_load_from_data(parser, contents, -1, NULL) && JSON_NODE_HOLDS_OBJECT(json_parser_get_root(parser))) {
        JsonObject *result = json_node_get_object(json_parser_get_root(parser));
        g_unlink(path);
        if (!g_strcmp0(text_member(result, "status"), "installed") && !g_strcmp0(text_member(result, "version"), CAGE_VERSION)) {
            update_set("current", "已更新到最新版本", FALSE);
            char *text = g_strdup_printf("Cage 已更新到 %s。", CAGE_VERSION);
            update_dialog(GTK_MESSAGE_INFO, text); g_free(text);
        } else {
            const char *message = text_member(result, "message");
            update_set("failed", *message ? message : "上次应用更新未完成", TRUE);
        }
    }
    g_free(contents); g_free(path); g_object_unref(parser);
    if (!update_json) update_set("idle", "", FALSE);
}
