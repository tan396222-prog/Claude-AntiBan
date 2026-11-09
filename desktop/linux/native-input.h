/* Host GTK input context. Guest text and surrounding content are never read. */
typedef struct {
    GtkIMContext *context;
    WebKitWebView *web;
    char *owner;
    gboolean composing, resetting;
    guint end_idle;
} CageNativeInput;

static CageNativeInput native_input;

static void input_emit(const char *kind, const char *text, int start, int end) {
    if (!native_input.owner) return;
    JsonObject *event = json_object_new();
    json_object_set_string_member(event, "owner", native_input.owner);
    json_object_set_string_member(event, "kind", kind);
    if (text) json_object_set_string_member(event, "text", text);
    json_object_set_int_member(event, "selectionStart", start);
    json_object_set_int_member(event, "selectionEnd", end);
    JsonNode *node = json_node_new(JSON_NODE_OBJECT);
    json_node_take_object(node, event);
    JsonGenerator *generator = json_generator_new();
    json_generator_set_root(generator, node);
    char *json = json_generator_to_data(generator, NULL);
    char *script = g_strdup_printf("window.dispatchEvent(new CustomEvent('cage-native-input',{detail:%s}));", json);
    webkit_web_view_evaluate_javascript(native_input.web, script, -1, NULL, NULL, NULL, NULL, NULL);
    g_free(script); g_free(json); json_node_free(node); g_object_unref(generator);
}

static void input_cancel(void) {
    if (native_input.end_idle) { g_source_remove(native_input.end_idle); native_input.end_idle = 0; }
    if (native_input.composing) input_emit("compositionend", "", 0, 0);
    native_input.composing = FALSE;
    native_input.resetting = TRUE;
    gtk_im_context_reset(native_input.context);
    native_input.resetting = FALSE;
}

static int input_utf16_length(const char *text, int characters) {
    int length = 0;
    for (const char *p = text; *p && characters-- > 0; p = g_utf8_next_char(p))
        length += g_utf8_get_char(p) > 0xffff ? 2 : 1;
    return length;
}

static void input_preedit(GtkIMContext *context, gpointer data) {
    (void)data;
    if (!native_input.owner || native_input.resetting) return;
    char *text = NULL;
    PangoAttrList *attributes = NULL;
    int cursor = 0;
    gtk_im_context_get_preedit_string(context, &text, &attributes, &cursor);
    if (text && *text) {
        if (native_input.end_idle) { g_source_remove(native_input.end_idle); native_input.end_idle = 0; }
        if (!native_input.composing) { native_input.composing = TRUE; input_emit("compositionstart", NULL, 0, 0); }
        int end = input_utf16_length(text, cursor);
        input_emit("compositionupdate", text, end, end);
    } else if (native_input.composing) {
        /* GTK sends an empty preedit before commit. preedit-end finalizes
         * cancellation after the synchronous commit signal has had a chance. */
    }
    if (attributes) pango_attr_list_unref(attributes);
    g_free(text);
}

static gboolean input_preedit_ended(gpointer data) {
    (void)data;
    native_input.end_idle = 0;
    if (native_input.composing) { native_input.composing = FALSE; input_emit("compositionend", "", 0, 0); }
    return G_SOURCE_REMOVE;
}
static void input_preedit_end(GtkIMContext *context, gpointer data) {
    (void)context; (void)data;
    if (!native_input.end_idle) native_input.end_idle = g_idle_add(input_preedit_ended, NULL);
}
static void input_commit(GtkIMContext *context, const char *text, gpointer data) {
    (void)context; (void)data;
    if (!native_input.owner || native_input.resetting) return;
    if (native_input.end_idle) { g_source_remove(native_input.end_idle); native_input.end_idle = 0; }
    input_emit(native_input.composing ? "compositionend" : "input", text, 0, 0);
    native_input.composing = FALSE;
}
static gboolean input_key(GtkWidget *widget, GdkEventKey *event, gpointer data) {
    (void)widget; (void)data;
    return native_input.owner && gtk_im_context_filter_keypress(native_input.context, event);
}
static gboolean input_focus_out(GtkWidget *widget, GdkEventFocus *event, gpointer data) {
    (void)widget; (void)event; (void)data;
    if (native_input.owner) {
        input_cancel(); input_emit("deactivated", NULL, 0, 0);
        gtk_im_context_focus_out(native_input.context);
        g_clear_pointer(&native_input.owner, g_free);
    }
    return FALSE;
}

static void input_initialize(WebKitWebView *web) {
    native_input.web = web;
    native_input.context = gtk_im_multicontext_new();
    gtk_im_context_set_use_preedit(native_input.context, TRUE);
    g_signal_connect(native_input.context, "preedit-changed", G_CALLBACK(input_preedit), NULL);
    g_signal_connect(native_input.context, "preedit-end", G_CALLBACK(input_preedit_end), NULL);
    g_signal_connect(native_input.context, "commit", G_CALLBACK(input_commit), NULL);
    g_signal_connect(web, "key-press-event", G_CALLBACK(input_key), NULL);
    g_signal_connect(web, "key-release-event", G_CALLBACK(input_key), NULL);
    g_signal_connect(web, "focus-out-event", G_CALLBACK(input_focus_out), NULL);
}

static gboolean input_handle(JsonObject *request) {
    const char *operation = json_object_has_member(request, "operation") ? json_object_get_string_member(request, "operation") : "";
    const char *owner = json_object_has_member(request, "owner") ? json_object_get_string_member(request, "owner") : "";
    if (!g_uuid_string_is_valid(owner)) return FALSE;
    if (!strcmp(operation, "activate")) {
        if (g_strcmp0(owner, native_input.owner)) { input_cancel(); g_free(native_input.owner); native_input.owner = g_strdup(owner); }
        gtk_im_context_set_client_window(native_input.context, gtk_widget_get_window(GTK_WIDGET(native_input.web)));
        gtk_im_context_focus_in(native_input.context);
    } else if (g_strcmp0(owner, native_input.owner)) return FALSE;
    if (!strcmp(operation, "activate") || !strcmp(operation, "caret")) {
        double scale = webkit_web_view_get_zoom_level(native_input.web);
        double x = json_object_get_double_member_with_default(request, "x", 0);
        double y = json_object_get_double_member_with_default(request, "y", 0);
        double height = json_object_get_double_member_with_default(request, "height", 24);
        if (!isfinite(x) || !isfinite(y) || !isfinite(height) || height <= 0 || fabs(x) > 32768 || fabs(y) > 32768 || height > 4096) return FALSE;
        GdkRectangle caret = {(int)(x * scale), (int)(y * scale), 0, MAX(1, (int)(height * scale))};
        gtk_im_context_set_cursor_location(native_input.context, &caret);
    } else if (!strcmp(operation, "cancel")) input_cancel();
    else if (!strcmp(operation, "deactivate")) {
        input_cancel(); gtk_im_context_focus_out(native_input.context);
        g_clear_pointer(&native_input.owner, g_free);
    } else return FALSE;
    return TRUE;
}
