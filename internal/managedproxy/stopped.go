package managedproxy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

// ConfirmStopped performs no cleanup. Linux and Windows remove their private
// process record only after stopping the identified process. launchd retains
// its plist even after bootout, so an existing plist needs a service lookup.
func ConfirmStopped(ctx context.Context, profile string) error {
	path := filepath.Join(Dir(profile), "managed-process.json")
	if runtime.GOOS == "darwin" {
		path = servicePath(profile)
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		name := "gui/" + strconv.Itoa(os.Getuid()) + "/" + label(profile)
		err := exec.CommandContext(ctx, "/bin/launchctl", "print", name).Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 113 {
			return nil
		} // service not found
	}
	return errors.New("无法确认此配置的代理进程已经退出")
}
