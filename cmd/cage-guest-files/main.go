// cage-guest-files runs as the guest GUI agent's own user. File selections are
// transient stdin data, never host paths or commands selected by a web page.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sylvinus/agent-vm/internal/clipboardfiles"
)

func run(args []string, input io.Reader, output io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("guest file operation invalid")
	}
	if args[0] == "cache" && len(args) == 2 {
		var request clipboardfiles.CacheRequest
		if json.Unmarshal([]byte(args[1]), &request) != nil {
			return errors.New("invalid clipboard cache request")
		}
		store, err := clipboardfiles.UserCacheStore()
		if err != nil {
			return err
		}
		result, err := store.Control(request)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	}
	var store clipboardfiles.CacheStore
	if args[0] != "export" {
		var err error
		store, err = clipboardfiles.UserCacheStore()
		if err != nil {
			return err
		}
	}
	if args[0] == "save" {
		if len(args) != 2 {
			return errors.New("file name required")
		}
		selection, err := store.Save(&framedInput{reader: input}, args[1])
		if err != nil {
			return err
		}
		return returnCachedSelection(output, store, selection)
	}
	if args[0] == "import" {
		selection, err := store.Read(input)
		if err != nil {
			return err
		}
		return returnCachedSelection(output, store, selection)
	}
	var paths []string
	if len(args) == 2 && args[1] != "--paths-stdin" {
		if len(args[1]) > 1<<20 || json.Unmarshal([]byte(args[1]), &paths) != nil {
			return errors.New("file selection invalid")
		}
	} else {
		data, err := io.ReadAll(io.LimitReader(input, (1<<20)+1))
		if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &paths) != nil {
			return errors.New("file selection invalid")
		}
	}
	switch args[0] {
	case "link-cached":
		linked, err := store.LinkCachedFiles(paths)
		if err != nil {
			return err
		}
		return returnCachedSelection(output, store, linked)
	case "export":
		return clipboardfiles.Write(output, paths)
	case "copy":
		copied, err := store.Copy(paths)
		if err != nil {
			return err
		}
		return returnCachedSelection(output, store, copied)
	default:
		return errors.New("guest file operation invalid")
	}
}

func returnCachedSelection(output io.Writer, store clipboardfiles.CacheStore, selection clipboardfiles.CachedSelection) error {
	if err := json.NewEncoder(output).Encode(selection); err != nil {
		_ = store.Release(selection.ID)
		return err
	}
	return nil
}

// The zero-length frame confirms that the sender finished the file. A closed
// connection before that marker must not publish a truncated attachment.
type framedInput struct {
	reader    io.Reader
	remaining uint32
	ended     bool
}

func (f *framedInput) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if f.ended {
		return 0, io.EOF
	}
	if f.remaining == 0 {
		var header [4]byte
		if _, err := io.ReadFull(f.reader, header[:]); err != nil {
			return 0, io.ErrUnexpectedEOF
		}
		f.remaining = binary.LittleEndian.Uint32(header[:])
		if f.remaining > 64<<10 {
			return 0, errors.New("file frame exceeds limit")
		}
		if f.remaining == 0 {
			f.ended = true
			return 0, io.EOF
		}
	}
	n, err := f.reader.Read(buffer[:min(len(buffer), int(f.remaining))])
	f.remaining -= uint32(n)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		// Do not expose selected file names or OS error strings in agent logs.
		fmt.Fprintln(os.Stderr, "Clipboard file transfer failed; check the selection, permissions and limits.")
		os.Exit(1)
	}
}
