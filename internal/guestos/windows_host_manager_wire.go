package guestos

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const windowsHostManagerVersion = 1
const windowsHostManagerWireLimit = 64 << 10

type windowsHostManagerRequest struct {
	Version   int                        `json:"version"`
	VMID      uuid.UUID                  `json:"vm_id"`
	Operation string                     `json:"operation"`
	MemoryGiB int                        `json:"memory_gib,omitempty"`
	CPUs      int                        `json:"cpus,omitempty"`
	Storage   *windowsHostStorageRequest `json:"storage,omitempty"`
}

type windowsStorageDisk struct {
	Path   string `json:"path"`
	Volume uint32 `json:"volume"`
	FileID uint64 `json:"file_id"`
}

type windowsVMStorageLayout struct {
	Configuration string `json:"configuration"`
	Snapshots     string `json:"snapshots"`
	Paging        string `json:"paging"`
}

type windowsHostStorageRequest struct {
	Action       string `json:"action"`
	ID           string `json:"id,omitempty"`
	SourceVolume uint32 `json:"source_volume,omitempty"`
	SourceFileID uint64 `json:"source_file_id,omitempty"`
	Disk         string `json:"disk,omitempty"`
}

type windowsHostStorageState struct {
	Disk    windowsStorageDisk      `json:"disk"`
	Layout  *windowsVMStorageLayout `json:"layout,omitempty"`
	Pending string                  `json:"pending,omitempty"`
	Outcome string                  `json:"outcome,omitempty"`
}

func validWindowsStorageID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func (r windowsHostStorageRequest) validate() error {
	if r.Action == "status" {
		if r.ID == "" && r.SourceVolume == 0 && r.SourceFileID == 0 && r.Disk == "" {
			return nil
		}
		return errors.New("invalid_request")
	}
	if !validWindowsStorageID(r.ID) || r.SourceVolume == 0 || r.SourceFileID == 0 {
		return errors.New("invalid_request")
	}
	switch r.Action {
	case "begin", "commit", "rollback":
		if r.Disk != "" {
			return errors.New("invalid_request")
		}
	case "attach":
		if r.Disk == "" || len(r.Disk) > 32700 || strings.ContainsAny(r.Disk, "\x00\r\n") {
			return errors.New("invalid_request")
		}
	default:
		return errors.New("invalid_request")
	}
	return nil
}

type windowsHostManagerResponse struct {
	Version int             `json:"version"`
	VMID    uuid.UUID       `json:"vm_id"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func (r windowsHostManagerRequest) validate() error {
	if r.Version != windowsHostManagerVersion || r.VMID == uuid.Nil {
		return errors.New("invalid_request")
	}
	if r.Operation == "storage" {
		if r.Storage == nil || r.MemoryGiB != 0 || r.CPUs != 0 {
			return errors.New("invalid_request")
		}
		return r.Storage.validate()
	}
	if r.Storage != nil {
		return errors.New("invalid_request")
	}
	switch r.Operation {
	case "inspect", "start", "shutdown":
		if r.MemoryGiB != 0 || r.CPUs != 0 {
			return errors.New("invalid_request")
		}
	case "resources":
		if r.MemoryGiB == 0 && r.CPUs == 0 {
			return nil
		}
		if r.MemoryGiB < 4 || r.MemoryGiB > 1024 || r.CPUs < 2 || r.CPUs > 1024 {
			return errors.New("invalid_request")
		}
	default:
		return errors.New("invalid_operation")
	}
	return nil
}
