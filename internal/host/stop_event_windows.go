package host

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func stopEventName(instanceDir string) (*uint16, error) {
	sid, err := privateSID()
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(sid.String() + "\x00" + strings.ToLower(filepath.Clean(instanceDir))))
	return windows.UTF16PtrFromString(fmt.Sprintf(`Local\CageHostagentStop-%x`, hash[:16]))
}

// ListenInstanceStop receives graceful-stop requests without a console.
// The event's DACL restricts notification to the current user and SYSTEM.
func ListenInstanceStop(instanceDir string, onStop func()) (func(), error) {
	name, err := stopEventName(instanceDir)
	if err != nil {
		return nil, err
	}
	sid, err := privateSID()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + sid.String() + ")(A;;GA;;;SY)")
	if err != nil {
		return nil, err
	}
	attrs := &windows.SecurityAttributes{SecurityDescriptor: sd}
	attrs.Length = uint32(unsafe.Sizeof(*attrs))
	event, err := windows.CreateEvent(attrs, 1, 0, name)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }); <-finished }
	go func() {
		defer close(finished)
		defer windows.CloseHandle(event)
		for {
			select {
			case <-done:
				return
			default:
			}
			result, err := windows.WaitForSingleObject(event, 250)
			if err != nil {
				onStop()
				return
			}
			if result == windows.WAIT_OBJECT_0 {
				onStop()
				<-done
				return
			}
		}
	}()
	return stop, nil
}

// RequestInstanceStop returns false when no Cage hostagent owns the event.
func RequestInstanceStop(instanceDir string) (bool, error) {
	name, err := stopEventName(instanceDir)
	if err != nil {
		return false, err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(event)
	return true, windows.SetEvent(event)
}
