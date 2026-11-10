//go:build darwin && arm64 && cgo && !no_vz

package vm

import (
	"errors"

	"github.com/Code-Hex/vz/v3"
)

func macOSRestoreResourceRequirements(image string) (MacOSResourceRequirements, error) {
	restore, err := vz.LoadMacOSRestoreImageFromPath(image)
	if err != nil {
		return MacOSResourceRequirements{}, err
	}
	return macOSImageResourceRequirements(restore)
}

func macOSImageResourceRequirements(restore *vz.MacOSRestoreImage) (MacOSResourceRequirements, error) {
	if restore == nil {
		return MacOSResourceRequirements{}, errors.New("missing macOS restore image")
	}
	requirements := restore.MostFeaturefulSupportedConfiguration()
	if requirements == nil || requirements.MinimumSupportedCPUCount() == 0 || requirements.MinimumSupportedMemorySize() == 0 {
		return MacOSResourceRequirements{}, errors.New("this macOS restore image has no supported configuration for this Mac")
	}
	model := requirements.HardwareModel()
	if model == nil || !model.Supported() || len(model.DataRepresentation()) == 0 {
		return MacOSResourceRequirements{}, errors.New("this Mac does not support the restore image's hardware model")
	}
	return MacOSResourceRequirements{Version: 1, CPUs: int(requirements.MinimumSupportedCPUCount()), MemoryGiB: int((requirements.MinimumSupportedMemorySize()-1)/(1<<30) + 1), Source: "restore-image"}, nil
}
