#import <Foundation/Foundation.h>
NS_ASSUME_NONNULL_BEGIN
@interface CMConnection : NSObject
+ (NSString *)defaultRelay;
@property(class, nonatomic, readonly) BOOL usesSimulatorIdentity;
+ (nullable NSString *)pairCode:(NSString *)code relay:(NSString *)relay error:(NSError **)error NS_SWIFT_NAME(pair(code:relay:));
+ (nullable NSString *)deviceFingerprint:(NSError **)error NS_SWIFT_NAME(deviceFingerprint());
+ (nullable instancetype)connectToRelay:(NSString *)relay room:(NSString *)room token:(NSString *)token
    pin:(NSString *)pin assets:(NSString *)assets error:(NSError **)error NS_SWIFT_NAME(connect(relay:room:token:pin:assets:));
+ (nullable instancetype)connectSavedRelay:(NSString *)relay lookup:(NSString *)lookup pin:(NSString *)pin
    assets:(NSString *)assets error:(NSError **)error NS_SWIFT_NAME(connectSaved(relay:lookup:pin:assets:));
@property(nonatomic, readonly) NSURL *launchURL;
- (void)close;
@end
NS_ASSUME_NONNULL_END
