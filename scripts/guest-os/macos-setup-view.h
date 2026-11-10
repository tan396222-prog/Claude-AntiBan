#import <AppKit/AppKit.h>
#import <Virtualization/Virtualization.h>

NS_ASSUME_NONNULL_BEGIN

// QA-only adapter for a VM the caller already owns. All calls, including the
// authorization check, run on the AppKit main thread. A revoked session closes.
@interface CageGuestSetupView : NSObject
- (nullable instancetype)initWithMachine:(VZVirtualMachine *)machine
                                    size:(NSSize)size
                         setupAuthorized:(BOOL (^)(void))setupAuthorized;
- (instancetype)init NS_UNAVAILABLE;
- (nullable NSData *)snapshotPNG;
- (nullable NSData *)snapshotJPEG;
- (BOOL)sendKey:(NSEvent *)event;
// Click in the view's logical coordinates, with the origin at its top left.
- (BOOL)clickAtX:(CGFloat)x y:(CGFloat)y;
- (BOOL)scrollAtX:(CGFloat)x y:(CGFloat)y deltaX:(CGFloat)deltaX deltaY:(CGFloat)deltaY;
- (void)close;
@end

NS_ASSUME_NONNULL_END
