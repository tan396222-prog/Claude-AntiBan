package cage

// Windows AF_UNIX sockets use the user's temp directory, never a drive root.
func gateTempDir() string { return "" }
