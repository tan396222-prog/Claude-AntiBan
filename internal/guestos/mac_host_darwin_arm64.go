//go:build darwin && arm64 && cgo && !no_vz

package guestos

/*
#cgo CFLAGS: -x objective-c -fno-objc-arc
#cgo LDFLAGS: -framework Foundation -framework Security -framework Virtualization
#import <Security/SecTask.h>
#import <Virtualization/Virtualization.h>
#include <stdint.h>

typedef struct {
	const char *error;
	const char *version;
	const char *build;
	const char *url;
	uint64_t cpus;
	uint64_t memory;
} cageMacRestore;

extern void cageCompleteMacRestore(uintptr_t handle, cageMacRestore result);

static bool cageHasVirtualizationEntitlement(void) {
	SecTaskRef task = SecTaskCreateFromSelf(NULL);
	if (!task) return false;
	CFTypeRef value = SecTaskCopyValueForEntitlement(task, CFSTR("com.apple.security.virtualization"), NULL);
	bool enabled = value && CFEqual(value, kCFBooleanTrue);
	if (value) CFRelease(value);
	CFRelease(task);
	return enabled;
}

// The Go binding's network lookup exposes only the URL. Apple's callback also
// contains the compatible hardware requirements, without reading the IPSW.
static void cageFetchMacRestore(uintptr_t handle) {
	@autoreleasepool {
		if (@available(macOS 12.0, *)) {
			if (!cageHasVirtualizationEntitlement()) {
				cageCompleteMacRestore(handle, (cageMacRestore){.error = "Cage must be signed with the com.apple.security.virtualization entitlement"});
				return;
			}
			if (!VZVirtualMachine.supported) {
				cageCompleteMacRestore(handle, (cageMacRestore){.error = "virtualization is unavailable on this Mac"});
				return;
			}
			[VZMacOSRestoreImage fetchLatestSupportedWithCompletionHandler:^(VZMacOSRestoreImage *image, NSError *error) {
				@autoreleasepool {
					if (error) {
						cageCompleteMacRestore(handle, (cageMacRestore){.error = error.localizedDescription.UTF8String});
						return;
					}
					VZMacOSConfigurationRequirements *config = image.mostFeaturefulSupportedConfiguration;
					if (!config.hardwareModel.supported) {
						cageCompleteMacRestore(handle, (cageMacRestore){.error = "no macOS restore image supports this Mac"});
						return;
					}
					NSOperatingSystemVersion os = image.operatingSystemVersion;
					NSString *version = [NSString stringWithFormat:@"%ld.%ld.%ld",
					    (long)os.majorVersion, (long)os.minorVersion, (long)os.patchVersion];
					cageCompleteMacRestore(handle, (cageMacRestore){
						.version = version.UTF8String,
						.build = image.buildVersion.UTF8String,
						.url = image.URL.absoluteString.UTF8String,
						.cpus = config.minimumSupportedCPUCount,
						.memory = config.minimumSupportedMemorySize,
					});
				}
			}];
		} else {
			cageCompleteMacRestore(handle, (cageMacRestore){.error = "macOS restore images require macOS 12 or later"});
		}
	}
}
*/
import "C"

import (
	"context"
	"errors"
	"runtime/cgo"
)

type macRestoreResult struct {
	image MacRestore
	err   error
}

func probeMacHost(ctx context.Context) (MacRestore, error) {
	if err := ctx.Err(); err != nil {
		return MacRestore{}, err
	}
	done := make(chan macRestoreResult, 1)
	C.cageFetchMacRestore(C.uintptr_t(cgo.NewHandle(done)))
	select {
	case <-ctx.Done():
		return MacRestore{}, ctx.Err()
	case result := <-done:
		return result.image, result.err
	}
}

// The framework owns the request until completion. A late callback releases
// the handle and can finish even when its caller already timed out.
//
//export cageCompleteMacRestore
func cageCompleteMacRestore(handle C.uintptr_t, native C.cageMacRestore) {
	h := cgo.Handle(handle)
	done := h.Value().(chan macRestoreResult)
	defer h.Delete()
	if native.error != nil {
		done <- macRestoreResult{err: errors.New(C.GoString(native.error))}
		return
	}
	done <- macRestoreResult{image: MacRestore{
		Version:            C.GoString(native.version),
		Build:              C.GoString(native.build),
		URL:                C.GoString(native.url),
		MinimumCPUs:        int(native.cpus),
		MinimumMemoryBytes: uint64(native.memory),
	}}
}
