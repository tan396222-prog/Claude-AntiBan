// Package spec defines shared-folder data without importing a VM runtime.
// Desktop connection clients validate project paths without bundling Lima.
package spec

type Mount struct {
	Location   string `json:"location"`
	MountPoint string `json:"mountPoint,omitempty"`
	Writable   bool   `json:"writable"`
	SSHFS      *SSHFS `json:"sshfs,omitempty"`
}

type SSHFS struct {
	SFTPDriver    string   `json:"sftpDriver,omitempty"`
	Cache         *bool    `json:"cache,omitempty"`
	ReadonlyNames []string `json:"readonlyNames,omitempty"`
}
