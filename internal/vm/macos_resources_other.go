//go:build !darwin || !arm64 || !cgo || no_vz

package vm

import "errors"

func macOSRestoreResourceRequirements(string) (MacOSResourceRequirements, error) {
	return MacOSResourceRequirements{}, errors.New("Apple 恢复镜像要求只能在 Apple Silicon Mac 上读取")
}
