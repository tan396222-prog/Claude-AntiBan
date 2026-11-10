package reversesshfs

import "io"

// ServeConfined serves only localPath over an already secured transport. It uses
// the same root and metadata protections as builtin reverse-sshfs mounts.
func ServeConfined(transport io.ReadWriteCloser, localPath string, readonly bool, readonlyNames []string) error {
	server, root, err := newRootedServer(transport, localPath, readonly, readonlyNames)
	if err != nil {
		return err
	}
	defer root.Close()
	defer server.Close()
	return server.Serve()
}

// ServeConfinedVirtual exposes the selected directory at / without disclosing
// its host path. All reads, writes and Guard decisions use the same confined
// root and metadata protections as ServeConfined.
func ServeConfinedVirtual(transport io.ReadWriteCloser, localPath string, readonly bool, readonlyNames []string) error {
	server, root, err := newRootedServerAt(transport, localPath, "/", readonly, readonlyNames)
	if err != nil {
		return err
	}
	defer root.Close()
	defer server.Close()
	return server.Serve()
}
