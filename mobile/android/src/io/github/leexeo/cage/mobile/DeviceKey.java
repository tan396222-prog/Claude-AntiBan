package io.github.leexeo.cage.mobile;

import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import cagemobile.DeviceSigner;
import java.security.KeyPairGenerator;
import java.security.KeyStore;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.spec.ECGenParameterSpec;

final class DeviceKey implements DeviceSigner {
    private static final String ALIAS = "cage-mobile-device-v1";
    private final KeyStore store;

    DeviceKey() throws Exception {
        store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        if (!store.containsAlias(ALIAS)) {
            KeyPairGenerator generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore");
            generator.initialize(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(new ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_NONE)
                .build());
            generator.generateKeyPair();
        }
    }

    @Override public byte[] publicKeyDER() throws Exception {
        return store.getCertificate(ALIAS).getPublicKey().getEncoded();
    }

    @Override public byte[] signSHA256(byte[] digest) throws Exception {
        if (digest == null || digest.length != 32) throw new IllegalArgumentException("Expected a SHA-256 digest");
        Signature signature = Signature.getInstance("NONEwithECDSA");
        signature.initSign((PrivateKey) store.getKey(ALIAS, null));
        signature.update(digest);
        return signature.sign();
    }
}
