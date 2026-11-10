package keepawake

import (
	"context"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var powerLibrary = windows.NewLazySystemDLL("kernel32.dll")
var powerCreate = powerLibrary.NewProc("PowerCreateRequest")
var powerSet = powerLibrary.NewProc("PowerSetRequest")
var powerClear = powerLibrary.NewProc("PowerClearRequest")

// Match REASON_CONTEXT's full union layout, including its detailed variant.
type reasonContext struct {
	Version, Flags          uint32
	SimpleReason            uintptr
	ResourceID, StringCount uint32
	Strings                 uintptr
}

func hold(ctx context.Context, ready func()) error {
	reason, err := windows.UTF16PtrFromString("Cage 远程连接待机")
	if err != nil {
		return err
	}
	value := reasonContext{Flags: 1, SimpleReason: uintptr(unsafe.Pointer(reason))}
	handle, _, callErr := powerCreate.Call(uintptr(unsafe.Pointer(&value)))
	runtime.KeepAlive(reason)
	if handle == ^uintptr(0) || handle == 0 {
		return fmt.Errorf("无法创建主机防休眠请求：%w", callErr)
	}
	defer windows.CloseHandle(windows.Handle(handle))
	// POWER_REQUEST_TYPE: SystemRequired=1, ExecutionRequired=3. No display or
	// away-mode request: monitors can sleep and explicit user sleep is respected.
	for _, kind := range []uintptr{1, 3} {
		ok, _, err := powerSet.Call(handle, kind)
		if ok == 0 {
			return fmt.Errorf("无法启用主机防休眠请求：%w", err)
		}
		defer powerClear.Call(handle, kind)
	}
	ready()
	<-ctx.Done()
	return ctx.Err()
}
