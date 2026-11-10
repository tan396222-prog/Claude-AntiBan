#import "macos-setup-bridge.h"
#import "macos-setup-view.h"

// Build-only QA overlay. Demonstrate handing the actual VM to the main thread
// and closing a session. It never publishes a framebuffer or accepts input.
void CageRunSetupBridge(void *machine, void *queue, double width, double height,
                        const char *title, bool controller) {
    (void)width; (void)height; (void)title; (void)controller;
    @autoreleasepool {
        if (!NSThread.isMainThread) {
            fprintf(stderr, "Cage setup probe requires the AppKit main thread\n");
            return;
        }
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyProhibited];
        VZVirtualMachine *vm = (__bridge VZVirtualMachine *)machine;
        __block BOOL allowed = YES;
        CageGuestSetupView *setup = [[CageGuestSetupView alloc] initWithMachine:vm
            size:NSMakeSize(1024, 640) setupAuthorized:^BOOL { return allowed; }];
        allowed = NO;
        BOOL revoked = setup && ![setup snapshotPNG] && ![setup clickAtX:1 y:1] && ![setup scrollAtX:1 y:1 deltaX:0 deltaY:10];
        fprintf(stderr, "Cage setup probe: main-thread binding and revocation %s\n", revoked ? "passed" : "failed");
        [setup close];

        // Preserve Lima's normal shutdown path while AppKit owns its main loop.
        dispatch_queue_t vmQueue = (__bridge dispatch_queue_t)queue;
        __block BOOL seenRunning = NO;
        NSTimer *timer = [NSTimer scheduledTimerWithTimeInterval:0.25 repeats:YES block:^(NSTimer *tick) {
            (void)tick;
            dispatch_async(vmQueue, ^{
                VZVirtualMachineState state = vm.state;
                if (state == VZVirtualMachineStateRunning) seenRunning = YES;
                if (seenRunning && (state == VZVirtualMachineStateStopped || state == VZVirtualMachineStateError)) {
                    dispatch_async(dispatch_get_main_queue(), ^{
                        [app stop:nil];
                        NSEvent *wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                            location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0
                            context:nil subtype:0 data1:0 data2:0];
                        [app postEvent:wake atStart:YES];
                    });
                }
            });
        }];
        [app run];
        [timer invalidate];
    }
}
