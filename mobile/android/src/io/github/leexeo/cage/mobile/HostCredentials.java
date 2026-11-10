package io.github.leexeo.cage.mobile;

import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.Arrays;
import java.security.MessageDigest;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

// The public address and verified fingerprint also authenticate the ciphertext.
// Keep the AES key separate from the P-256 device identity so forgetting a host
// leaves the device fingerprint intact.
final class HostCredentials {
    private static final String ALIAS = "cage-mobile-saved-host-aes-v1";
    private static final String SEALED = "sealed-lookup-v1";
    private final SharedPreferences prefs;
    HostCredentials(SharedPreferences prefs) { this.prefs = prefs; }
    boolean saved() { return prefs.contains(SEALED); }
    boolean matches(String relay, String pin) { return relay.equals(prefs.getString("relay", "")) && pin.equals(prefs.getString("pin", "")); }
    private static String entry(String relay, String pin) throws Exception {
        byte[] digest = MessageDigest.getInstance("SHA-256").digest(context(relay, pin));
        return "host-lookup-v2-" + Base64.encodeToString(digest, Base64.NO_WRAP | Base64.URL_SAFE);
    }

    private static KeyStore store() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        return store;
    }
    private SecretKey key(boolean create) throws Exception {
        KeyStore store = store();
        if (store.containsAlias(ALIAS)) return (SecretKey) store.getKey(ALIAS, null);
        if (!create) throw new IOException("设备凭据不可用。");
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
            .setKeySize(256).setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build());
        return generator.generateKey();
    }
    private static byte[] context(String relay, String pin) {
        return ("Cage saved host v1\n" + relay + "\n" + pin).getBytes(StandardCharsets.UTF_8);
    }
    synchronized void save(String relay, String pin, String lookup) throws Exception {
        SharedPreferences.Editor change = prefs.edit().putString("relay", relay).putString("pin", pin);
        String previousRelay = prefs.getString("relay", ""), previousPin = prefs.getString("pin", "");
        if (prefs.contains(SEALED)) change.putString(entry(previousRelay, previousPin), prefs.getString(SEALED, ""));
        if (lookup != null) {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.ENCRYPT_MODE, key(true));
            cipher.updateAAD(context(relay, pin));
            byte[] plain = lookup.getBytes(StandardCharsets.UTF_8);
            byte[] encrypted;
            try { encrypted = cipher.doFinal(plain); } finally { Arrays.fill(plain, (byte) 0); }
            String value = Base64.encodeToString(cipher.getIV(), Base64.NO_WRAP) + "." + Base64.encodeToString(encrypted, Base64.NO_WRAP);
            change.putString(SEALED, value).putString(entry(relay, pin), value);
        } else if (!matches(relay, pin)) {
            String saved = prefs.getString(entry(relay, pin), null);
            if (saved == null) change.remove(SEALED); else change.putString(SEALED, saved);
        }
        if (!change.commit()) throw new IOException("无法保存主机。");
    }
    synchronized String lookup(String relay, String pin) throws Exception {
        String value = matches(relay,pin) ? prefs.getString(SEALED, "") : prefs.getString(entry(relay, pin), "");
        String[] parts = value.split("\\.", -1);
        if (parts.length != 2) throw new IOException("无可用重连凭据。");
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, key(false), new GCMParameterSpec(128, Base64.decode(parts[0], Base64.NO_WRAP)));
        cipher.updateAAD(context(relay, pin));
        byte[] plain = cipher.doFinal(Base64.decode(parts[1], Base64.NO_WRAP));
        try {
            String token = StandardCharsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(plain)).toString();
            if (!Invitation.capability(token)) throw new IOException("重连凭据无效。");
            return token;
        } finally { Arrays.fill(plain, (byte) 0); }
    }
    synchronized void forget() throws Exception {
        // Other computers share the encryption key. Forget only the selected host.
        if (!prefs.edit().remove(entry(prefs.getString("relay", ""), prefs.getString("pin", "")))
                .remove(SEALED).remove("relay").remove("pin").commit()) throw new IOException("无法移除主机。");
    }
}
