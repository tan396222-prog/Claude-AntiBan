package io.github.leexeo.cage.mobile;

import android.app.Activity;
import android.app.AlertDialog;
import android.app.Dialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.AssetManager;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.content.res.ColorStateList;
import android.view.Gravity;
import android.net.Uri;
import android.os.Bundle;
import android.os.Build;
import android.text.InputType;
import android.text.Editable;
import android.text.TextWatcher;
import android.view.View;
import android.webkit.CookieManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.*;
import cagemobile.Cagemobile;
import cagemobile.Client;
import java.io.*;
import java.time.Instant;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import org.json.JSONObject;
import org.json.JSONArray;
import java.util.LinkedHashMap;
import java.util.Map;
import androidx.webkit.WebViewCompat;
import androidx.webkit.WebViewFeature;
import androidx.webkit.ScriptHandler;

public final class MainActivity extends Activity {
    private static final int PICK_INVITATION = 41;
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private SharedPreferences trust;
    private HostCredentials credentials;
    private DeviceKey device;
    private String fingerprint = "";
    private Invitation invitation;
    private Invitation.Saved imported;
    private WebView browser;
    private final Map<String, ConnectedDevice> connections = new LinkedHashMap<>();
    private ConnectedDevice activeDevice;
    private boolean viewingWorkspace;
    private boolean openArchivedAfterConnect;
    private static final class ConnectedDevice {
        final String id;
        final String hostKey;
        final Client client;
        final Uri launch;
        WebView view;
        ScriptHandler script;
        String name;
        ConnectedDevice(String hostKey, Client client, Uri launch, String name) {
            this.id=java.util.UUID.randomUUID().toString(); this.hostKey=hostKey;
            this.client=client; this.launch=launch; this.name=name;
        }
    }
    private enum Page { HOME, SETTINGS, HOST, RELAY, PAIR, CODE }
    private Page page = Page.HOME;
    private FrameLayout root;
    private LinearLayout form;
    private FrameLayout home;
    private View overlay;
    private EditText relayField, pinField;
    private TextView localPin, status, inviteStatus;
    private CheckBox verified;
    private Switch customRelay;
    private Button connect, forgetHost;
    private boolean busy;
    private int generation;
    private MobileTheme palette;
    private SharedPreferences appearance;
    private static final int SCAN = 42;

    private void rememberRelay(String relay) {
        trust.edit().putString("preferred-relay", relay).apply();
    }
    private String preferredRelay() {
        return trust.getString("preferred-relay", trust.getString("relay", Cagemobile.defaultRelay()));
    }
    private static String deviceModel() {
        String model = Build.MODEL.trim();
        String brand = Build.MANUFACTURER.trim();
        if (brand.isEmpty() || model.regionMatches(true, 0, brand, 0, brand.length())) return model;
        return Character.toUpperCase(brand.charAt(0)) + brand.substring(1) + " " + model;
    }
    private String selectedRelay() throws Exception {
        return customRelay.isChecked() ? Invitation.relay(relayField.getText().toString()) : Cagemobile.defaultRelay();
    }
    private void displayRelay(String value) {
        boolean custom = !Cagemobile.defaultRelay().equals(value);
        customRelay.setChecked(custom);
        relayField.setText(custom ? value : "");
        relayField.setVisibility(custom ? View.VISIBLE : View.GONE);
    }
    private void pairScan(String code) {
        if (busy) return;
        if (device == null) { status.setText("设备密钥尚未就绪，请稍后重试。"); return; }
        String relay = preferredRelay();
        int attempt = ++generation;
        setConnecting(true);
        status.setText("正在与电脑配对…");
        worker.execute(() -> {
            try {
                Invitation.Saved saved = new Invitation.Saved(new JSONObject(Cagemobile.pair(code, relay, device)));
                credentials.save(saved.relay, saved.pin, saved.lookup);
                rememberRelay(saved.relay);
                runOnUiThread(() -> {
                    if (generation != attempt) return;
                    invitation = null;
                    imported = null;
                    setConnecting(false);
                    connect();
                });
            } catch (Exception e) {
                runOnUiThread(() -> {
                    if (generation != attempt) return;
                    setConnecting(false);
                    status.setText("配对未完成。请确认两端使用同一中继，再用电脑上新生成的二维码或配对码重试。");
                });
            }
        });
    }

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        trust = getSharedPreferences("verified-host", MODE_PRIVATE);
        appearance = getSharedPreferences("appearance", MODE_PRIVATE);
        palette = MobileTheme.apply(this, themeMode());
        credentials = new HostCredentials(trust);
        showSetup();
        if (state != null) {
            String restoredPage = state.getString("page", Page.HOME.name());
            Page restored = "ENVIRONMENT".equals(restoredPage) ? Page.HOME : Page.valueOf(restoredPage);
            if (restored == Page.PAIR || restored == Page.CODE) showPair(restored == Page.CODE);
            if (restored == Page.SETTINGS) showSettings(false);
            if (restored == Page.HOST || restored == Page.RELAY) showAdvanced(restored);
        }
        worker.execute(() -> {
            try {
                DeviceKey key = new DeviceKey();
                String pin = Cagemobile.fingerprint(key);
                runOnUiThread(() -> {
                    if (isDestroyed()) return;
                    device = key;
                    fingerprint = pin;
                    if (localPin != null) localPin.setText(pin);
                });
            } catch (Exception e) {
                runOnUiThread(() -> {
                    if (!isDestroyed()) status.setText("无法使用 Android Keystore 设备密钥。");
                });
            }
        });
    }
    @Override protected void onSaveInstanceState(Bundle saved) {
        super.onSaveInstanceState(saved);
        saved.putString("page", page.name());
    }

    private int dp(int value) { return Math.round(getResources().getDisplayMetrics().density * value); }
    private String themeMode() { return appearance.getString("theme", "system"); }
    private void insets(View root) {
        root.setOnApplyWindowInsetsListener((v, w) -> {
            v.setPadding(w.getSystemWindowInsetLeft(), w.getSystemWindowInsetTop(), w.getSystemWindowInsetRight(), w.getSystemWindowInsetBottom());
            return w.consumeSystemWindowInsets();
        });
        root.requestApplyInsets();
    }
    private LinearLayout column() {
        LinearLayout group = new LinearLayout(this);
        group.setOrientation(LinearLayout.VERTICAL);
        return group;
    }
    private GradientDrawable surface(int color, int radius, boolean outline) {
        GradientDrawable shape = new GradientDrawable();
        shape.setColor(color);
        shape.setCornerRadius(dp(radius));
        if (outline) shape.setStroke(dp(1), palette.line);
        return shape;
    }
    private Drawable pressable(int color, int radius, boolean outline) {
        return new RippleDrawable(ColorStateList.valueOf(palette.dark ? 0x14ffffff : 0x0d000000), surface(color, radius, outline), null);
    }
    private TextView text(String value, int size, int color) {
        TextView view = new TextView(this);
        view.setText(value);
        view.setTextSize(size);
        view.setTextColor(color);
        return view;
    }
    private TextView label(String value, int size) {
        TextView view = text(value, size, palette.muted);
        view.setPadding(0, dp(12), 0, dp(3));
        form.addView(view);
        return view;
    }
    private Button button(LinearLayout parent, String name, boolean primary, View.OnClickListener action) {
        Button view = new Button(this);
        view.setText(name);
        view.setAllCaps(false);
        view.setTextSize(16);
        view.setTextColor(primary ? palette.onAction : palette.ink);
        view.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        view.setStateListAnimator(null);
        view.setBackground(pressable(primary ? palette.action : palette.soft, 14, false));
        view.setOnClickListener(action);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-1, dp(52));
        lp.topMargin = dp(8);
        parent.addView(view, lp);
        return view;
    }
    private ImageButton icon(String resource, String name, View.OnClickListener action) {
        ImageButton view = new ImageButton(this);
        view.setImageResource(getResources().getIdentifier("cage_" + resource, "drawable", getPackageName()));
        view.setContentDescription(name);
        view.setColorFilter(palette.ink);
        view.setScaleType(ImageView.ScaleType.CENTER);
        view.setBackground(pressable(palette.soft, 24, false));
        view.setOnClickListener(action);
        return view;
    }
    private View connectComputerButton(View.OnClickListener action) {
        LinearLayout view = new LinearLayout(this);
        view.setOrientation(LinearLayout.HORIZONTAL);
        view.setGravity(Gravity.CENTER);
        view.setPadding(dp(16), 0, dp(16), 0);
        view.setBackground(pressable(palette.action, 14, false));
        view.setFocusable(true);
        view.setContentDescription("连接电脑");
        view.setOnClickListener(action);
        view.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View host, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(host, info);
                info.setClassName(Button.class.getName());
            }
        });
        ImageView computer = image("cage_computer");
        computer.setColorFilter(palette.onAction);
        view.addView(computer, dp(22), dp(22));
        TextView title = text("连接电脑", 16, palette.onAction);
        title.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        title.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        LinearLayout.LayoutParams caption = new LinearLayout.LayoutParams(-2, -2);
        caption.setMarginStart(dp(10));
        view.addView(title, caption);
        return view;
    }
    private EditText field(String hint, String value) {
        EditText input = new EditText(this);
        input.setSingleLine();
        input.setTextSize(16);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        input.setHint(hint);
        input.setText(value);
        form.addView(input);
        return input;
    }
    private void statusView() {
        status = label("", 14);
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        status.setMinHeight(dp(22));
    }
    private void layout(Page next) {
        page = next;
        overlay = null;
        root = new FrameLayout(this);
        root.setBackgroundColor(palette.bg);
        setContentView(root);
        insets(root);
        localPin = null;
    }
    private ImageView image(String resource) {
        ImageView img = new ImageView(this);
        img.setImageResource(getResources().getIdentifier(resource, "drawable", getPackageName()));
        img.setScaleType(ImageView.ScaleType.FIT_CENTER);
        img.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        return img;
    }
    private void showSetup() {
        viewingWorkspace = false;
        if (browser != null) browser.onPause();
        layout(Page.HOME);
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);
        root.addView(scroll, -1, -1);
        FrameLayout body = new FrameLayout(this);
        body.setMinimumHeight(dp(440));
        scroll.addView(body, new ScrollView.LayoutParams(-1, -2));
        home = body;
        FrameLayout.LayoutParams gear = new FrameLayout.LayoutParams(dp(48), dp(48), Gravity.TOP | Gravity.RIGHT);
        gear.setMargins(dp(18), dp(12), dp(18), 0);
        body.addView(icon("gear", "设置", v -> showSettings(false)), gear);

        form = column();
        form.setGravity(Gravity.CENTER);
        LinearLayout homeForm = form;
        // Balance the larger brand/action gap so the logo stays in place (§3.6).
        form.setPadding(0, dp(32), 0, 0);
        FrameLayout.LayoutParams hero = new FrameLayout.LayoutParams(dp(276), -2, Gravity.CENTER);
        hero.leftMargin = dp(24);
        hero.rightMargin = dp(24);
        body.addView(form, hero);
        ImageView mark = image("cage_icon");
        mark.setBackground(surface(palette.panel, 18, false));
        mark.setClipToOutline(true);
        form.addView(mark, dp(120), dp(120));
        TextView title = text("Cage", 38, palette.ink);
        title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        title.setGravity(Gravity.CENTER);
        title.setPadding(0, dp(8), 0, dp(52));
        form.addView(title);
        LinearLayout.LayoutParams action = new LinearLayout.LayoutParams(-1, dp(60));
        action.topMargin = dp(8);
        form.addView(connectComputerButton(v -> showPair(false)), action);
        statusView();
        body.addOnLayoutChangeListener((view, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom) -> {
            int height = bottom - top;
            if (height == oldBottom - oldTop && right - left == oldRight - oldLeft) return;
            hero.width = Math.min(dp(276), Math.max(0, right - left - dp(48)));
            homeForm.setLayoutParams(hero);
            int spacing = Math.min(dp(60), Math.max(dp(28), Math.round(height * 0.075f))) + dp(32);
            title.setPadding(0, dp(8), 0, spacing - dp(8));
        });
    }
    private void closeSheet() {
        if (busy) return;
        showSetup();
    }
    private void showPair(boolean code) {
        boolean opening = overlay == null;
        if (overlay != null) root.removeView(overlay);
        page = code ? Page.CODE : Page.PAIR;
        home.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
        home.setDescendantFocusability(android.view.ViewGroup.FOCUS_BLOCK_DESCENDANTS);
        FrameLayout layer = new FrameLayout(this);
        overlay = layer;
        root.addView(layer, -1, -1);
        View shade = new View(this);
        shade.setBackgroundColor(0x64000000);
        shade.setOnClickListener(v -> closeSheet());
        shade.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        shade.setFocusable(false);
        layer.addView(shade, -1, -1);
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(false);
        scroll.setBackground(surface(palette.panel, 26, false));
        scroll.setClickable(true);
        FrameLayout.LayoutParams sheet = new FrameLayout.LayoutParams(Math.min(getResources().getDisplayMetrics().widthPixels, dp(500)), -2,
            Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL);
        sheet.setMargins(dp(8), dp(12), dp(8), dp(8));
        sheet.width = Math.min(sheet.width - dp(16), dp(500));
        layer.addView(scroll, sheet);
        form = column();
        form.setPadding(dp(20), dp(14), dp(20), dp(14));
        scroll.addView(form);
        FrameLayout heading = new FrameLayout(this);
        form.addView(heading, -1, dp(48));
        TextView title = text(code ? "配对码" : "连接电脑", 24, palette.ink);
        title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        heading.addView(title, new FrameLayout.LayoutParams(-2, -2, Gravity.LEFT | Gravity.CENTER_VERTICAL));
        if (!code) {
            form.addView(image("pair_devices"), -1, dp(112));
            label("在电脑的「设置 → 远程连接」中生成配对二维码或配对码。", 14);
            button(form, "扫码连接", true, v -> startActivityForResult(new Intent(this, ScanActivity.class), SCAN));
            button(form, "使用配对码", false, v -> showPair(true));
        } else {
            label("粘贴电脑上复制的一次性配对码。", 15);
            EditText manualCode = field("一次性配对码", "");
            button(form, "连接电脑", true, v -> {
                String value = manualCode.getText().toString().trim();
                if (value.isEmpty()) { status.setText("请粘贴配对码。"); return; }
                manualCode.setText("");
                android.view.inputmethod.InputMethodManager ime = (android.view.inputmethod.InputMethodManager)getSystemService(INPUT_METHOD_SERVICE);
                ime.hideSoftInputFromWindow(manualCode.getWindowToken(), 0);
                pairScan(value);
            });
            button(form, "返回配对方式", false, v -> showPair(false));
        }
        if (!code && !connections.isEmpty()) {
            label("已连接的设备", 15);
            label("长按设备可取消连接", 14);
            for (ConnectedDevice connected : connections.values()) {
                Button entry = button(form, connected.name + "  ·  进入", false, v -> enterDevice(connected));
                entry.setOnLongClickListener(v -> {confirmDisconnect(connected); return true;});
            }
        }
        if (credentials.saved() && !code) button(form, "重新连接已配对电脑", false, v -> connect());
        statusView();
        form.setFocusableInTouchMode(true);
        form.requestFocus();
        if (opening && android.animation.ValueAnimator.areAnimatorsEnabled()) {
            shade.setAlpha(0f);
            scroll.getViewTreeObserver().addOnPreDrawListener(new android.view.ViewTreeObserver.OnPreDrawListener() {
                @Override public boolean onPreDraw() {
                    scroll.getViewTreeObserver().removeOnPreDrawListener(this);
                    if (overlay != layer) return true;
                    // Start below the viewport after measuring, before the first visible frame.
                    scroll.setTranslationY(scroll.getHeight() + sheet.bottomMargin);
                    scroll.animate().translationY(0).setDuration(300)
                        .setInterpolator(new android.view.animation.PathInterpolator(0.2f, 0f, 0f, 1f)).start();
                    shade.animate().alpha(1f).setDuration(200).start();
                    return true;
                }
            });
        }
    }

    private void settingsPage(Page next, String name, Runnable back) {
        layout(next);
        ScrollView scroll = new ScrollView(this);
        root.addView(scroll, -1, -1);
        form = column();
        form.setPadding(dp(22), dp(14), dp(22), dp(28));
        form.setFocusableInTouchMode(true);
        scroll.addView(form);
        form.addView(icon("back", "返回", v -> back.run()), dp(48), dp(48));
        TextView title = text(name, 32, palette.ink);
        title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        title.setPadding(0, dp(26), 0, dp(18));
        form.addView(title);
    }
    private Button row(String title, View.OnClickListener listener) {
        Button b = button(form, title, false, listener);
        b.setGravity(Gravity.CENTER_VERTICAL | Gravity.START);
        b.setPadding(dp(20), 0, dp(16), 0);
        b.setBackground(pressable(palette.panel, 14, true));
        return b;
    }
    private static final String[] THEMES = {"system", "light", "dark"};
    private static final String[] THEME_NAMES = {"跟随系统", "浅色", "深色"};
    private void showSettings(boolean focusTheme) {
        settingsPage(Page.SETTINGS, "设置", this::showSetup);
        label("外观", 14);
        int index = java.util.Arrays.asList(THEMES).indexOf(themeMode());
        LinearLayout options = column();
        String current = "主题   ·   " + THEME_NAMES[Math.max(0, index)];
        Button chooser = row(current + "   ▾", v -> {
            boolean opening = options.getVisibility() != View.VISIBLE;
            options.setVisibility(opening ? View.VISIBLE : View.GONE);
            ((Button)v).setText(current + (opening ? "   ▴" : "   ▾"));
        });
        form.addView(options);
        options.setVisibility(View.GONE);
        for (int i = 0; i < THEMES.length; i++) {
            String value = THEMES[i];
            Button choice = button(options, (i == index ? "✓   " : "") + THEME_NAMES[i], false, v -> {
                appearance.edit().putString("theme", value).apply();
                palette = MobileTheme.apply(this, value);
                showSettings(!v.isInTouchMode());
            });
            choice.setSelected(i == index);
        }
        label("远程连接", 14);
        row("已归档会话", v -> openArchivedSessions());
        row("中继设置", v -> showAdvanced(Page.RELAY));
        row("主机与指纹", v -> showAdvanced(Page.HOST));
        statusView();
        if (focusTheme) chooser.requestFocus();
    }

    private void showAdvanced(Page next) {
        boolean host = next == Page.HOST;
        settingsPage(next, host ? "主机与指纹" : "中继设置", () -> showSettings(false));
        verified = new CheckBox(this);
        customRelay = new Switch(this);
        customRelay.setText("使用自建中继");
        form.addView(customRelay, -1, dp(48));
        relayField = field("自建 VPS 的 HTTPS 地址", "");
        customRelay.setOnCheckedChangeListener((view, enabled) -> {
            relayField.setVisibility(enabled ? View.VISIBLE : View.GONE);
            verified.setChecked(false);
        });
        displayRelay(imported != null && host ? imported.relay : preferredRelay());
        if (!host) {
            label("关闭自建中继后，使用 Cage 官方中继。", 14);
            button(form, "保存中继设置", true, v -> {
                try { rememberRelay(selectedRelay()); status.setText("中继设置已保存。"); }
                catch (Exception e) { status.setText("请填写有效的 HTTPS 地址。"); }
            });
            statusView();
            return;
        }
        label("这台手机的指纹", 15);
        localPin = label(fingerprint.isEmpty() ? "正在读取设备密钥…" : fingerprint, 14);
        localPin.setTypeface(Typeface.MONOSPACE);
        localPin.setTextIsSelectable(true);
        button(form, "复制手机指纹", false, v -> {
            if (fingerprint.isEmpty()) return;
            ClipboardManager clipboard = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            clipboard.setPrimaryClip(ClipData.newPlainText("Cage 手机指纹", fingerprint));
            status.setText("手机指纹已复制，请在电脑上核对并信任。");
        });
        label("电脑指纹", 15);
        pinField = field("sha256:…", imported != null ? imported.pin : trust.getString("pin", ""));
        verified.setText("我已与电脑上独立显示的指纹核对");
        form.addView(verified);
        TextWatcher changed = new TextWatcher() {
            public void beforeTextChanged(CharSequence s, int start, int count, int after) {}
            public void onTextChanged(CharSequence s, int start, int before, int count) { verified.setChecked(false); }
            public void afterTextChanged(Editable text) {}
        };
        pinField.addTextChangedListener(changed);
        relayField.addTextChangedListener(changed);
        button(form, "保存已核对的主机", true, v -> saveVerified());
        forgetHost = button(form, "忘记主机", false, v -> new AlertDialog.Builder(this)
            .setMessage("移除这台电脑的地址、指纹和重连凭据？")
            .setNegativeButton("取消", null).setPositiveButton("忘记主机", (dialog, which) -> forget()).show());
        forgetHost.setVisibility(trust.contains("pin") ? View.VISIBLE : View.GONE);
        inviteStatus = label(imported != null ? "请与电脑独立显示的指纹核对，再保存主机。" :
            invitation != null ? "已导入本次临时邀请。" :
            credentials.saved() ? "这台电脑已保存，可以直接重新连接。" : "导入电脑生成的连接文件或临时邀请。", 14);
        button(form, "导入连接文件", false, v -> {
            Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
            startActivityForResult(intent, PICK_INVITATION);
        });
        connect = button(form, credentials.saved() ? "重新连接" : "连接工作台", false, v -> connect());
        statusView();
    }

    private void saveVerified() {
        if (!verified.isChecked()) { status.setText("请先在电脑上核对指纹。"); return; }
        final String relay, pin;
        try { relay = selectedRelay(); pin = Invitation.pin(pinField.getText().toString()); }
        catch (Exception e) { status.setText("中继地址或电脑指纹格式无效。"); return; }
        Invitation.Saved record = imported;
        if (record != null && (!record.relay.equals(relay) || !record.pin.equals(pin))) { status.setText("填写内容与导入的连接文件不一致，请重新核对。"); return; }
        int attempt = ++generation;
        setConnecting(true);
        worker.execute(() -> {
            try {
                credentials.save(relay, pin, record == null ? null : record.lookup);rememberRelay(relay);
                runOnUiThread(() -> { if (generation == attempt) {
                    imported = null; verified.setChecked(false); setConnecting(false); forgetHost.setVisibility(View.VISIBLE);
                    connect.setText(credentials.saved() && invitation == null ? "重新连接" : "连接工作台");
                    if (credentials.saved() && invitation == null) inviteStatus.setText("这台电脑已保存，可以直接重新连接。");
                    status.setText(credentials.saved() ? "已保存这台电脑，可以重新连接。" : "已保存主机指纹。");
                } });
            } catch (Exception e) { runOnUiThread(() -> { if (generation == attempt) { setConnecting(false); status.setText("无法安全保存主机，请解锁手机后重试。"); } }); }
        });
    }
    private void forget() {
        int attempt = ++generation; setConnecting(true);
        worker.execute(() -> {
            try {
                credentials.forget();
                runOnUiThread(() -> { if (generation == attempt) { invitation=null; imported=null; setConnecting(false); showAdvanced(Page.HOST); status.setText("已忘记主机。"); } });
            } catch (Exception e) { runOnUiThread(() -> { if (generation == attempt) { setConnecting(false); status.setText("移除未完成，请重试。"); } }); }
        });
    }

    @Override protected void onActivityResult(int code, int result, Intent data) {
        super.onActivityResult(code, result, data);
        if (code == SCAN) {
            if(result==RESULT_OK && data!=null) pairScan(data.getStringExtra("code"));
            else if(data!=null && data.hasExtra("error"))status.setText(data.getStringExtra("error"));
            return;
        }
        if (code != PICK_INVITATION || result != RESULT_OK || data == null || data.getData() == null) return;
        Uri file = data.getData();
        if (!"content".equals(file.getScheme())) { status.setText("请选择设备文件应用中的邀请。"); return; }
        int attempt = ++generation;
        invitation = null; imported = null; verified.setChecked(false); setConnecting(true);
        worker.execute(() -> {
            try (InputStream input = getContentResolver().openInputStream(file)) {
                JSONObject object = Invitation.read(input);
                Invitation.Saved saved = object.has("version") ? new Invitation.Saved(object) : null;
                Invitation item = saved == null ? Invitation.room(object) : null;
                runOnUiThread(() -> { if (attempt == generation) {
                    setConnecting(false); invitation = item; imported = saved; status.setText("");
                    if (saved != null) {
                        displayRelay(saved.relay); pinField.setText(saved.pin); verified.setChecked(false);
                        inviteStatus.setText("请与电脑独立显示的指纹核对，再保存主机。");
                    } else { inviteStatus.setText("已导入本次临时邀请。"); }
                    connect.setText("连接工作台");
                } });
            } catch (Exception e) {
                runOnUiThread(() -> { if (attempt == generation) { setConnecting(false); inviteStatus.setText("文件无效或临时邀请已过期，请重新导入。"); } });
            }
        });
    }

    private void connect() {
        connect(false);
    }
    private void connect(boolean archivedEntry) {
        if (busy) return;
        openArchivedAfterConnect = archivedEntry;
        final String relay, pin;
        try {
            relay = page == Page.HOST ? selectedRelay() : trust.getString("relay", preferredRelay());
            pin = Invitation.pin(page == Page.HOST ? pinField.getText().toString() : trust.getString("pin", ""));
        } catch (Exception e) { status.setText("请在「设置 → 主机与指纹」保存电脑指纹。"); return; }
        if ((page == Page.HOST && imported != null) || !credentials.matches(relay,pin)) { status.setText("请先核对并保存这台主机。"); return; }
        ConnectedDevice existing = connections.get(relay + "\n" + pin);
        if (existing != null) {
            enterDevice(existing);
            if (openArchivedAfterConnect) {
                openArchivedAfterConnect = false;
                browser.evaluateJavascript("window.dispatchEvent(new Event('cage-open-archives'))", null);
            }
            return;
        }
        if (device == null) { status.setText("设备密钥尚未就绪。"); return; }
        Invitation item = page == Page.HOST ? invitation : null;
        if (item == null && !credentials.saved()) { status.setText("请导入连接文件或有效的临时邀请。"); return; }
        if (item != null && !item.expiry.isAfter(Instant.now())) { invitation=null; status.setText("临时邀请已过期，请重新连接或导入新邀请。"); return; }
        invitation = null;
        if (item != null) inviteStatus.setText("本次邀请已用于连接。");
        int attempt = ++generation; setConnecting(true); status.setText("正在验证电脑并连接…");
        worker.execute(() -> {
            Client opened = null;
            try {
                String directory = extractUI();
                opened = item == null
                    ? Cagemobile.openSaved(relay, credentials.lookup(relay, pin), pin, device)
                    : Cagemobile.open(relay, item.room, item.token, pin, device);
                if (item == null) opened.reportDevice("android", deviceModel());
                String launch = opened.startUI(directory);
                Uri url = Uri.parse(launch);
                if (!"http".equals(url.getScheme()) || !"127.0.0.1".equals(url.getHost()) || url.getPort()<1 || url.getUserInfo()!=null) throw new IOException();
                Client connected = opened;
                runOnUiThread(() -> {
                    if (attempt != generation || isDestroyed()) { close(connected); return; }
                    setConnecting(false);
                    ConnectedDevice next = new ConnectedDevice(relay + "\n" + pin, connected, url,
                        "电脑 " + (connections.size() + 1));
                    connections.put(next.hostKey, next);
                    createBrowser(next);
                    enterDevice(next);
                    broadcastDevices();
                });
            } catch (Exception e) {
                close(opened);
                runOnUiThread(() -> { if (attempt == generation) {
                    setConnecting(false); if (page == Page.HOST) connect.setText(credentials.saved() ? "重新连接" : "连接工作台");
                    status.setText(item == null ? "连接未完成。请确认电脑上的连接服务正在运行，再点重新连接。若已更换凭据，请导入新的连接文件。" : "连接未完成。请核对双方指纹与网络，再导入新邀请。");
                } });
            }
        });
    }
    private void setConnecting(boolean connecting) {
        busy = connecting;
        enableActions(root, !connecting);
    }
    private static void enableActions(View view, boolean enabled) {
        if (view instanceof android.view.ViewGroup) {
            android.view.ViewGroup group = (android.view.ViewGroup)view;
            for (int i = 0; i < group.getChildCount(); i++) enableActions(group.getChildAt(i), enabled);
        } else if (view.isClickable() || view instanceof EditText) view.setEnabled(enabled);
    }
    private String extractUI() throws IOException {
        File root = new File(getCacheDir(), "cage-mobile-ui");
        copyAssets(getAssets(), "mobile-ui", root);
        return root.getAbsolutePath();
    }
    private static void copyAssets(AssetManager assets, String path, File dest) throws IOException {
        String[] children = assets.list(path);
        if (children == null) throw new IOException("missing bundled assets");
        if (children.length == 0) {
            try (InputStream input = assets.open(path); OutputStream output = new FileOutputStream(dest)) {
                byte[] bytes = new byte[16384]; int n;
                while ((n=input.read(bytes))!=-1) output.write(bytes,0,n);
            }
            return;
        }
        if (!dest.isDirectory() && !dest.mkdirs()) throw new IOException("asset storage unavailable");
        for (String child: children) copyAssets(assets,path+"/"+child,new File(dest,child));
    }
    private static boolean allowed(Uri url, int port) {
        return "http".equals(url.getScheme()) && "127.0.0.1".equals(url.getHost()) && url.getPort() == port && url.getUserInfo() == null;
    }
    private JSONObject deviceSnapshot(ConnectedDevice current) {
        JSONObject state = new JSONObject();
        JSONArray devices = new JSONArray();
        try {
            for (ConnectedDevice connected : connections.values()) {
                JSONObject item = new JSONObject();
                item.put("id", connected.id); item.put("name", connected.name); devices.put(item);
            }
            state.put("current", current.id); state.put("devices", devices);
        } catch (org.json.JSONException impossible) {throw new IllegalStateException(impossible);}
        return state;
    }
    private String deviceScript(ConnectedDevice current) {
        return "window.__cageDevices=" + deviceSnapshot(current).toString() +
            ";window.dispatchEvent(new CustomEvent('cage-devices',{detail:window.__cageDevices}));";
    }
    private void broadcastDevices() {
        for (ConnectedDevice connected : connections.values()) {
            if (connected.script != null) connected.script.remove();
            if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT))
                connected.script = WebViewCompat.addDocumentStartJavaScript(connected.view, deviceScript(connected),
                    java.util.Collections.singleton("http://127.0.0.1:" + connected.launch.getPort()));
            if (connected.view.getUrl() != null && allowed(Uri.parse(connected.view.getUrl()), connected.launch.getPort()))
                connected.view.evaluateJavascript(deviceScript(connected), null);
        }
    }
    private void deviceCommand(ConnectedDevice source, String json) {
        if (!connections.containsValue(source) || isDestroyed()) return;
        try {
            JSONObject command = new JSONObject(json);
            String action = command.optString("action");
            if ("name".equals(action)) {
                String name = command.optString("name").trim();
                if (!name.isEmpty() && name.length() <= 120 && !name.equals(source.name)) {
                    source.name = name; broadcastDevices();
                }
                return;
            }
            if (!viewingWorkspace || source != activeDevice) return;
            if ("home".equals(action)) showSetup();
        } catch (org.json.JSONException ignored) {}
    }
    private void createBrowser(ConnectedDevice connected) {
        WebView view = new WebView(this);
        connected.view = view;
        WebView.setWebContentsDebuggingEnabled(false);
        WebSettings settings = view.getSettings(); settings.setJavaScriptEnabled(true); settings.setAllowFileAccess(false); settings.setAllowContentAccess(false);
        settings.setUserAgentString(settings.getUserAgentString() + " CageMobile/1");
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW); settings.setCacheMode(WebSettings.LOAD_NO_CACHE);
        CookieManager.getInstance().setAcceptThirdPartyCookies(view,false);
        final int port = connected.launch.getPort();
        java.util.Set<String> origins = java.util.Collections.singleton("http://127.0.0.1:" + port);
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(view, "CageDevices", origins, (web, message, origin, mainFrame, reply) -> {
                if (message.getType() == androidx.webkit.WebMessageCompat.TYPE_STRING && mainFrame &&
                        allowed(origin, port) && web.getUrl() != null && allowed(Uri.parse(web.getUrl()), port))
                    runOnUiThread(() -> deviceCommand(connected, message.getData()));
            });
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT))
            connected.script = WebViewCompat.addDocumentStartJavaScript(view, deviceScript(connected), origins);
        view.setWebViewClient(new WebViewClient() {
            @Override public void onPageFinished(WebView w, String url) {
                if (allowed(Uri.parse(url), port)) w.evaluateJavascript(deviceScript(connected), null);
            }
            @Override public boolean shouldOverrideUrlLoading(WebView w, WebResourceRequest r) {
                String command = r.getUrl().toString();
                if (r.isForMainFrame() && r.hasGesture() && w.getUrl() != null && allowed(Uri.parse(w.getUrl()), port)) {
                    if (command.equals("cage://home")) { w.post(() -> showSetup()); return true; }
                }
                return !allowed(r.getUrl(),port);
            }
            @Override public WebResourceResponse shouldInterceptRequest(WebView w, WebResourceRequest r) {
                if (allowed(r.getUrl(),port)) return null;
                return new WebResourceResponse("text/plain","UTF-8",403,"Forbidden",java.util.Collections.emptyMap(),new ByteArrayInputStream(new byte[0]));
            }
        });
        Uri launch = connected.launch;
        if (openArchivedAfterConnect) {
            launch = launch.buildUpon().appendQueryParameter("page", "archived").build();
            openArchivedAfterConnect = false;
        }
        view.loadUrl(launch.toString());
    }
    private void openArchivedSessions() {
        if (activeDevice != null && connections.containsValue(activeDevice)) {
            enterDevice(activeDevice);
            browser.evaluateJavascript("window.dispatchEvent(new Event('cage-open-archives'))", null);
        } else {
            connect(true);
        }
    }
    private void enterDevice(ConnectedDevice selected) {
        if (!connections.containsValue(selected)) return;
        if (browser != null && browser != selected.view) browser.onPause();
        activeDevice = selected; browser = selected.view; viewingWorkspace = true;
        LinearLayout pane = column();
        if (browser.getParent() instanceof android.view.ViewGroup)
            ((android.view.ViewGroup)browser.getParent()).removeView(browser);
        pane.addView(browser, new LinearLayout.LayoutParams(-1,0,1));
        setContentView(pane); insets(pane); browser.onResume(); broadcastDevices();
    }
    private void confirmDisconnect(ConnectedDevice selected) {
        Dialog dialog = new Dialog(this);
        LinearLayout panel = column(); panel.setPadding(dp(22),dp(18),dp(22),dp(22));
        panel.setBackground(surface(palette.panel,26,false));
        TextView title = text("取消与“" + selected.name + "”的连接？",20,palette.ink);
        title.setTypeface(Typeface.DEFAULT,Typeface.BOLD); panel.addView(title);
        TextView explanation = text("设备将从当前连接列表移除，已保存的配对信息仍然保留。",14,palette.muted);
        explanation.setPadding(0,dp(12),0,dp(12)); panel.addView(explanation);
        button(panel,"取消连接",true,v -> {dialog.dismiss(); disconnectDevice(selected);});
        button(panel,"继续连接",false,v -> dialog.dismiss());
        dialog.setContentView(panel);
        android.view.Window window = dialog.getWindow();
        if (window != null) {
            window.setBackgroundDrawableResource(android.R.color.transparent);
            window.setGravity(Gravity.BOTTOM);
            window.setLayout(-1,-2);
        }
        dialog.show();
        if (window != null) window.setLayout(-1,-2);
    }
    private static void close(Client c) { if (c!=null) try { c.close(); } catch (Exception ignored) { } }
    private void disconnectDevice(ConnectedDevice connected) {
        if (connections.remove(connected.hostKey) == null) return;
        if (connected.script != null) connected.script.remove();
        connected.view.stopLoading();
        if (connected.view.getParent() instanceof android.view.ViewGroup)
            ((android.view.ViewGroup)connected.view.getParent()).removeView(connected.view);
        connected.view.destroy();
        worker.execute(() -> close(connected.client));
        if (activeDevice == connected) {
            browser = null; activeDevice = null;
            if (viewingWorkspace) showSetup();
        }
        if (page == Page.PAIR) showPair(false);
        broadcastDevices();
    }
    private void endSession() {
        ++generation; invitation = null;
        for (ConnectedDevice connected : new java.util.ArrayList<>(connections.values())) {
            if (connected.script != null) connected.script.remove();
            connected.view.stopLoading(); connected.view.destroy();
            worker.execute(() -> close(connected.client));
        }
        connections.clear(); browser = null; activeDevice = null;
    }
    @Override protected void onPause() {
        if (viewingWorkspace && browser != null) browser.onPause();
        super.onPause();
    }
    @Override protected void onResume() {
        super.onResume();
        if (viewingWorkspace && browser != null) browser.onResume();
    }
    @Override public void onBackPressed() {
        if (busy) return;
        if (viewingWorkspace && browser != null && browser.canGoBack()) browser.goBack();
        else if (viewingWorkspace) showSetup();
        else if (page == Page.CODE) showPair(false);
        else if (page == Page.RELAY || page == Page.HOST) showSettings(false);
        else if (page != Page.HOME) showSetup();
        else super.onBackPressed();
    }
    @Override public void onDestroy() { endSession(); worker.shutdown(); super.onDestroy(); }
}
