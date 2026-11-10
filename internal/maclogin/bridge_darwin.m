//go:build darwin && arm64 && cgo && !no_vz
#import <Cocoa/Cocoa.h>
#import <IOKit/hidsystem/IOLLEvent.h>
#import "bridge.h"
#import "../../scripts/guest-os/macos-setup-view.m"

static VZVirtualMachine *machine;
static CageGuestSetupView *loginView;
// Match the virtual Mac's scanout. The input protocol retains its original
// 16:10 coordinate system, regardless of host window size or display scaling.
static const CGFloat scanWidth = 1920, scanHeight = 1200;

void cageLoginLoop(void *ref) {
    @autoreleasepool {
        machine = (__bridge VZVirtualMachine *)ref;
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyProhibited];
        [app run];
        [loginView close]; loginView = nil; machine = nil;
    }
}
void cageLoginStop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp stop:nil];
        [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint
            modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:YES];
    });
}
void cageLoginEnable(bool enabled) {
    dispatch_sync(dispatch_get_main_queue(), ^{
        [loginView close]; loginView = nil;
        if (enabled && machine) loginView = [[CageGuestSetupView alloc] initWithMachine:machine size:NSMakeSize(scanWidth,scanHeight)
            setupAuthorized:^BOOL { return machine != nil; }];
    });
}
void *cageLoginImage(int *size) {
    __block void *copy = NULL;
    dispatch_sync(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSData *image = [loginView snapshotJPEG];
            if (image.length && image.length <= (4<<20)) {
                copy = malloc(image.length);
                if (copy) { memcpy(copy, image.bytes, image.length); *size = (int)image.length; }
            }
        }
    });
    return copy;
}
void *cageLoginImageRegion(int x, int y, int width, int height, int *size) {
    if (x < 0 || y < 0 || width <= 0 || height <= 0 || x + width > 1024 || y + height > 640) return NULL;
    __block void *copy = NULL;
    dispatch_sync(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            NSData *frame = [loginView snapshotJPEG];
            if (!frame.length) return;
            NSBitmapImageRep *source = [NSBitmapImageRep imageRepWithData:frame];
            if (!source.CGImage) return;
            CGRect region = CGRectMake(x * source.pixelsWide / 1024.0, y * source.pixelsHigh / 640.0,
                                       width * source.pixelsWide / 1024.0, height * source.pixelsHigh / 640.0);
            CGImageRef cropped = CGImageCreateWithImageInRect(source.CGImage, region);
            if (!cropped) return;
            NSBitmapImageRep *output = [[NSBitmapImageRep alloc] initWithCGImage:cropped];
            CGImageRelease(cropped);
            NSData *image = [output representationUsingType:NSBitmapImageFileTypeJPEG properties:@{NSImageCompressionFactor:@0.92}];
            if (image.length && image.length <= (4<<20)) {
                copy = malloc(image.length);
                if (copy) { memcpy(copy, image.bytes, image.length); *size = (int)image.length; }
            }
        }
    });
    return copy;
}
bool cageLoginClick(int x, int y) {
    __block BOOL ok = NO;
    dispatch_sync(dispatch_get_main_queue(), ^{ ok = [loginView clickAtX:x * scanWidth / 1024.0 y:y * scanHeight / 640.0]; });
    return ok;
}
bool cageLoginKey(int code, bool shift) {
    __block BOOL ok = NO;
    dispatch_sync(dispatch_get_main_queue(), ^{
        if (!loginView) return;
        // A hardware modifier carries both the aggregate flag and the left/right
        // device bit. The offscreen view has no host keyboard state to inherit.
        NSEventModifierFlags flags = shift ? NSEventModifierFlagShift | NX_DEVICELSHIFTKEYMASK : 0;
        NSEvent *(^event)(NSEventType, int, NSEventModifierFlags) = ^NSEvent *(NSEventType kind, int key, NSEventModifierFlags mods) {
            return [NSEvent keyEventWithType:kind location:NSZeroPoint modifierFlags:mods timestamp:NSProcessInfo.processInfo.systemUptime
                windowNumber:0 context:nil characters:@"" charactersIgnoringModifiers:@"" isARepeat:NO keyCode:(unsigned short)key];
        };
        if (shift) {
            [loginView sendKey:event(NSEventTypeFlagsChanged,56,flags)];
            [loginView sendKey:event(NSEventTypeKeyDown,56,flags)];
        }
        ok = [loginView sendKey:event(NSEventTypeKeyDown,code,flags)] && [loginView sendKey:event(NSEventTypeKeyUp,code,flags)];
        if (shift) {
            [loginView sendKey:event(NSEventTypeKeyUp,56,0)];
            [loginView sendKey:event(NSEventTypeFlagsChanged,56,0)];
        }
    });
    return ok;
}
bool cageLoginSelectAll(void) {
    __block BOOL ok = NO;
    dispatch_sync(dispatch_get_main_queue(), ^{
        if (!loginView) return;
        NSEventModifierFlags command = NSEventModifierFlagCommand | NX_DEVICELCMDKEYMASK;
        NSEvent *(^event)(NSEventType, int, NSEventModifierFlags) = ^NSEvent *(NSEventType kind, int key, NSEventModifierFlags flags) {
            return [NSEvent keyEventWithType:kind location:NSZeroPoint modifierFlags:flags timestamp:NSProcessInfo.processInfo.systemUptime
                windowNumber:0 context:nil characters:@"" charactersIgnoringModifiers:@"" isARepeat:NO keyCode:(unsigned short)key];
        };
        [loginView sendKey:event(NSEventTypeFlagsChanged,55,command)];
        [loginView sendKey:event(NSEventTypeKeyDown,55,command)];
        ok = [loginView sendKey:event(NSEventTypeKeyDown,0,command)] && [loginView sendKey:event(NSEventTypeKeyUp,0,command)];
        [loginView sendKey:event(NSEventTypeKeyUp,55,0)];
        [loginView sendKey:event(NSEventTypeFlagsChanged,55,0)];
    });
    return ok;
}
bool cageLoginScroll(int x, int y, int deltaX, int deltaY) {
    __block BOOL ok = NO;
    dispatch_sync(dispatch_get_main_queue(), ^{
        ok = [loginView scrollAtX:x * scanWidth / 1024.0 y:y * scanHeight / 640.0
            deltaX:deltaX * scanWidth / 1024.0 deltaY:deltaY * scanHeight / 640.0];
    });
    return ok;
}
