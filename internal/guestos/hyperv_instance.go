package guestos

// HyperVInspection describes only the VM explicitly requested by its caller.
// A matched VM is not automatically owned by the current Cage profile.
type HyperVInspection struct {
	Inspection
	VMID         string               `json:"vmId"`
	Lookup       string               `json:"lookup"` // found, not-found (for this user), or unavailable
	LookupDetail string               `json:"lookupDetail,omitempty"`
	VM           *HyperVConfiguration `json:"vm,omitempty"`
}

type HyperVConfiguration struct {
	State               string `json:"state"`
	Generation          int    `json:"generation"`
	DiskCount           int    `json:"diskCount"`
	SingleLocalVHDX     *bool  `json:"singleLocalVhdx"` // null if local disk metadata was unreadable
	DiskType            string `json:"diskType,omitempty"`
	SecureBoot          bool   `json:"secureBoot"`
	SecureBootTemplate  string `json:"secureBootTemplate,omitempty"`
	VirtualTPM          bool   `json:"virtualTpm"`
	NetworkAdapterCount int    `json:"networkAdapterCount"`
}
