#import "NativeConnection.h"
#import <Cagemobile/Cagemobile.h>
#import <Security/Security.h>
#import <TargetConditionals.h>
#import <sys/utsname.h>
#import <UIKit/UIKit.h>

#if TARGET_OS_SIMULATOR && CAGE_SIMULATOR_DEBUG
static const BOOL simulatorIdentity = YES;
#else
static const BOOL simulatorIdentity = NO;
#endif

static void setError(NSError **error, NSString *text) {
    if (error) *error = [NSError errorWithDomain:@"CageMobile" code:1 userInfo:@{NSLocalizedDescriptionKey:text}];
}

@interface CMDeviceKey : NSObject <CagemobileDeviceSigner>
- (instancetype)initWithError:(NSError **)error;
@end

@implementation CMDeviceKey {
    SecKeyRef _key;
}
- (instancetype)initWithError:(NSError **)error {
    self = [super init];
    if (!self) return nil;
    NSString *name = simulatorIdentity ? @"io.github.lee-xeo.claude-cage.mobile.simulator.v1" : @"io.github.lee-xeo.claude-cage.mobile.device.v1";
    NSData *tag = [name dataUsingEncoding:NSUTF8StringEncoding];
    NSMutableDictionary *query = [@{(__bridge id)kSecClass:(__bridge id)kSecClassKey,
        (__bridge id)kSecAttrApplicationTag:tag, (__bridge id)kSecAttrKeyType:(__bridge id)kSecAttrKeyTypeECSECPrimeRandom,
        (__bridge id)kSecAttrKeyClass:(__bridge id)kSecAttrKeyClassPrivate,
        (__bridge id)kSecReturnRef:@YES} mutableCopy];
    if (!simulatorIdentity) query[(__bridge id)kSecAttrTokenID] = (__bridge id)kSecAttrTokenIDSecureEnclave;
    @synchronized ([CMDeviceKey class]) {
        CFTypeRef found = NULL;
        OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &found);
        if (status == errSecSuccess) { _key = (SecKeyRef)found; return self; }
        if (status != errSecItemNotFound) { setError(error, @"无法读取设备密钥。请先解锁 iPhone。"); return nil; }
        CFErrorRef failure = NULL;
        NSMutableDictionary *privateAttrs = [@{(__bridge id)kSecAttrIsPermanent:@YES,
            (__bridge id)kSecAttrApplicationTag:tag} mutableCopy];
        NSMutableDictionary *attrs = [@{(__bridge id)kSecAttrKeyType:(__bridge id)kSecAttrKeyTypeECSECPrimeRandom,
            (__bridge id)kSecAttrKeySizeInBits:@256, (__bridge id)kSecPrivateKeyAttrs:privateAttrs} mutableCopy];
        if (simulatorIdentity) {
            privateAttrs[(__bridge id)kSecAttrAccessible] = (__bridge id)kSecAttrAccessibleWhenUnlockedThisDeviceOnly;
        } else {
            SecAccessControlRef access = SecAccessControlCreateWithFlags(NULL, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                kSecAccessControlPrivateKeyUsage, &failure);
            if (!access) {
                if (failure) CFRelease(failure);
                setError(error, @"无法设置设备密钥访问权限。"); return nil;
            }
            attrs[(__bridge id)kSecAttrTokenID] = (__bridge id)kSecAttrTokenIDSecureEnclave;
            privateAttrs[(__bridge id)kSecAttrAccessControl] = CFBridgingRelease(access);
        }
        _key = SecKeyCreateRandomKey((__bridge CFDictionaryRef)attrs, &failure);
        if (failure) CFRelease(failure);
        if (!_key) { setError(error, @"设备安全芯片无法创建设备密钥。请使用已解锁的 iPhone。"); return nil; }
    }
    return self;
}
- (NSData *)publicKeyDER:(NSError **)error {
    SecKeyRef publicKey = SecKeyCopyPublicKey(_key);
    if (!publicKey) { setError(error, @"无法取得设备公钥。"); return nil; }
    CFErrorRef failure = NULL;
    NSData *point = CFBridgingRelease(SecKeyCopyExternalRepresentation(publicKey, &failure));
    CFRelease(publicKey);
    if (failure) CFRelease(failure);
    if (point.length != 65 || ((const uint8_t *)point.bytes)[0] != 4) {
        setError(error, @"设备公钥格式无效。"); return nil;
    }
    // RFC 5480 SubjectPublicKeyInfo header for an uncompressed P-256 point.
    static const uint8_t header[] = {0x30,0x59,0x30,0x13,0x06,0x07,0x2a,0x86,0x48,0xce,0x3d,0x02,0x01,
         0x06,0x08,0x2a,0x86,0x48,0xce,0x3d,0x03,0x01,0x07,0x03,0x42,0x00};
    NSMutableData *der = [NSMutableData dataWithBytes:header length:sizeof(header)];
    [der appendData:point];
    return der;
}
- (NSData *)signSHA256:(NSData *)digest error:(NSError **)error {
    if (digest.length != 32) { setError(error, @"签名摘要长度无效。"); return nil; }
    CFErrorRef failure = NULL;
    NSData *signature = CFBridgingRelease(SecKeyCreateSignature(_key, kSecKeyAlgorithmECDSASignatureDigestX962SHA256,
        (__bridge CFDataRef)digest, &failure));
    if (failure) CFRelease(failure);
    if (!signature) setError(error, @"设备密钥签名失败。");
    return signature;
}
- (void)dealloc { if (_key) CFRelease(_key); }
@end

@implementation CMConnection {
    CagemobileClient *_client;
}
+ (instancetype)workspaceWithClient:(CagemobileClient *)client assets:(NSString *)assets error:(NSError **)error {
    NSError *failure = nil;
    NSString *entry = [client startUI:assets error:&failure];
    NSURLComponents *url = [NSURLComponents componentsWithString:entry];
    if (failure || ![url.scheme isEqualToString:@"http"] || ![url.host isEqualToString:@"127.0.0.1"] ||
        url.port.integerValue < 1 || url.port.integerValue > 65535 || url.user || url.password || !url.URL) {
        [client close:nil]; setError(error, @"无法打开手机工作台。"); return nil;
    }
    CMConnection *result = [[CMConnection alloc] init];
    result->_client = client; result->_launchURL = url.URL;
    return result;
}
+ (NSString *)pairCode:(NSString *)code relay:(NSString *)relay error:(NSError **)error {
    CMDeviceKey *device = [[CMDeviceKey alloc] initWithError:error];
    if (!device) return nil;
    NSError *failure = nil;
    NSString *result = CagemobilePair(code, relay, device, &failure);
    if (failure || !result.length) {setError(error, @"配对未完成。请确认两端使用同一中继，再扫描新二维码。"); return nil;}
    return result;
}
+ (BOOL)usesSimulatorIdentity { return simulatorIdentity; }
+ (NSString *)defaultRelay { return CagemobileDefaultRelay(); }

+ (NSString *)deviceFingerprint:(NSError **)error {
    CMDeviceKey *device = [[CMDeviceKey alloc] initWithError:error];
    if (!device) return nil;
    NSError *failure = nil;
    NSString *pin = CagemobileFingerprint(device,&failure);
    if (failure || !pin.length) { setError(error, @"无法读取设备指纹。"); return nil; }
    return pin;
}
+ (instancetype)connectToRelay:(NSString *)relay room:(NSString *)room token:(NSString *)token
    pin:(NSString *)pin assets:(NSString *)assets error:(NSError **)error {
    CMDeviceKey *device = [[CMDeviceKey alloc] initWithError:error];
    if (!device) return nil;
    NSError *failure = nil;
    CagemobileClient *client = CagemobileOpen(relay, room, token, pin, device, &failure);
    if (!client || failure) { setError(error, @"连接未完成。请核对中继、双方指纹，并使用新邀请。"); return nil; }
    return [self workspaceWithClient:client assets:assets error:error];
}
+ (instancetype)connectSavedRelay:(NSString *)relay lookup:(NSString *)lookup pin:(NSString *)pin
    assets:(NSString *)assets error:(NSError **)error {
    CMDeviceKey *device = [[CMDeviceKey alloc] initWithError:error];
    if (!device) return nil;
    NSError *failure = nil;
    CagemobileClient *client = CagemobileOpenSaved(relay, lookup, pin, device, &failure);
    if (!client || failure) { setError(error, @"连接未完成。请确认电脑上的连接服务正在运行，再点重新连接。若已更换凭据，请导入新的连接文件。"); return nil; }
    struct utsname hardware;
    NSString *model = uname(&hardware) == 0 ? [NSString stringWithUTF8String:hardware.machine] : nil;
#if TARGET_OS_SIMULATOR
    model = NSProcessInfo.processInfo.environment[@"SIMULATOR_MODEL_IDENTIFIER"] ?: model;
#endif
    [client reportDevice:@"ios" model:model ?: UIDevice.currentDevice.model];
    return [self workspaceWithClient:client assets:assets error:error];
}
- (void)close {
    CagemobileClient *client;
    @synchronized (self) { client = _client; _client = nil; }
    [client close:nil];
}
- (void)dealloc { [self close]; }
@end
