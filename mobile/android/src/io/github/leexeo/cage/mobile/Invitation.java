package io.github.leexeo.cage.mobile;

import android.util.Base64;
import org.json.JSONObject;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Instant;

final class Invitation {
    final String room, token;
    final Instant expiry;

    private Invitation(String room, String token, Instant expiry) {
        this.room = room;
        this.token = token;
        this.expiry = expiry;
    }

    static final class Saved {
        final String relay, pin, lookup;
        Saved(JSONObject object) throws Exception {
            if (object.length() != 4 || !Integer.valueOf(1).equals(object.opt("version"))) throw new IOException("连接文件版本无效。");
            relay = relay(object.getString("relay"));
            pin = pin(object.getString("host_pin"));
            lookup = object.getString("lookup_token");
            if (!capability(lookup)) throw new IOException("连接文件无效。");
        }
    }

    static JSONObject read(InputStream stream) throws Exception {
        if (stream == null) throw new IOException("无法读取邀请文件。");
        ByteArrayOutputStream buffer = new ByteArrayOutputStream();
        byte[] chunk = new byte[1024];
        int count;
        while ((count = stream.read(chunk)) != -1) {
            if (buffer.size() + count > 4096) throw new IOException("邀请文件过大。");
            buffer.write(chunk, 0, count);
        }
        return new JSONObject(new String(buffer.toByteArray(), StandardCharsets.UTF_8));
    }

    static boolean capability(String value) {
        if (!value.matches("[A-Za-z0-9_-]{43}")) return false;
        int flags = Base64.URL_SAFE | Base64.NO_PADDING | Base64.NO_WRAP;
        byte[] bytes = Base64.decode(value, flags);
        return bytes.length == 32 && value.equals(Base64.encodeToString(bytes, flags));
    }

    static Invitation room(JSONObject object) throws Exception {
        String room = object.getString("id"), token = object.getString("client_token");
        Instant expiry = Instant.parse(object.getString("expires_at"));
        if (object.length() != 3 || !capability(room) || !capability(token) || !expiry.isAfter(Instant.now())) {
            throw new IOException("邀请无效或已过期。");
        }
        return new Invitation(room, token, expiry);
    }

    static String relay(String value) throws Exception {
        URI uri = new URI(value.trim());
        if (!"https".equalsIgnoreCase(uri.getScheme()) || uri.getHost() == null ||
            uri.getRawUserInfo() != null || uri.getRawQuery() != null || uri.getRawFragment() != null ||
            !(uri.getRawPath().isEmpty() || uri.getRawPath().equals("/")) || uri.getPort() == 0 || uri.getPort() > 65535) {
            throw new IllegalArgumentException("请填写 HTTPS 中继地址，地址中只保留主机和端口。");
        }
        return new URI("https", null, uri.getHost(), uri.getPort(), null, null, null).toASCIIString();
    }

    static String pin(String value) {
        String pin = value.trim();
        if (pin.startsWith("sha256:")) {
            String encoded = pin.substring(7);
            try {
                byte[] bytes = Base64.decode(encoded, Base64.NO_WRAP);
                if (bytes.length == 32 && encoded.equals(Base64.encodeToString(bytes, Base64.NO_WRAP))) return pin;
            } catch (IllegalArgumentException ignored) { }
        }
        throw new IllegalArgumentException("电脑指纹需要完整的 sha256: 值。");
    }
}
