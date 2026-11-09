package version

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sync"
)

// ExecutableIdentity is fixed when first read. A running service must not adopt
// the identity of a replacement subsequently installed at the same path.
var ExecutableIdentity = sync.OnceValues(func() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
})
