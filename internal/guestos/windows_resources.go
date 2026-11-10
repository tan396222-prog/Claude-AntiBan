package guestos

import (
	"context"
	_ "embed"
)

// WindowsVMResourceCeiling is the limit enforced by the Hyper-V management service.
func WindowsVMResourceCeiling(hostMemoryGiB, hostCPUs int) (memoryGiB, cpus int) {
	return min(256, hostMemoryGiB*3/4), min(64, hostCPUs)
}

type WindowsVMResourceState struct {
	State          string `json:"state"`
	CPUs           int    `json:"cpus"`
	MemoryStartup  uint64 `json:"memory_startup_bytes"`
	MemoryMinimum  uint64 `json:"memory_minimum_bytes"`
	MemoryMaximum  uint64 `json:"memory_maximum_bytes"`
	MemoryAssigned uint64 `json:"memory_assigned_bytes"`
	DynamicMemory  bool   `json:"dynamic_memory"`
}

// MatchesAllocation checks the Hyper-V configuration applied on a cold start.
func (s WindowsVMResourceState) MatchesAllocation(memoryGiB, cpus int) bool {
	return s.DynamicMemory && s.MemoryMinimum == 4<<30 && s.MemoryStartup == 4<<30 &&
		s.MemoryMaximum == uint64(memoryGiB)<<30 && s.CPUs == cpus
}

//go:embed windows_resources.ps1
var windowsVMResourcesScript string

func WindowsVMResources(ctx context.Context, root string, memoryGiB, cpus int) (WindowsVMResourceState, error) {
	return windowsVMResources(ctx, root, memoryGiB, cpus)
}
