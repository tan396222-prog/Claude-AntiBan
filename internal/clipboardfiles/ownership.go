package clipboardfiles

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
)

// CachedSelection records a directory created by a successful receive or copy.
// The identifier is private to the native clipboard owner.
type CachedSelection struct {
	ID    string   `json:"cache_id"`
	Paths []string `json:"paths"`
}

type cacheRecord struct {
	CachedSelection
	Directory string `json:"directory"`
	Marker    string `json:"marker"`
	Owner     int    `json:"owner"`
	Published bool   `json:"published"`
	Pending   bool   `json:"pending,omitempty"`
	Producer  int    `json:"producer,omitempty"`
}

type CacheStore struct{ Directory string }

type CacheRequest struct {
	Action string `json:"action"`
	ID     string `json:"cache_id,omitempty"`
}

func (store CacheStore) lock() (func(), error) {
	if err := host.EnsurePrivateDir(store.Directory); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(store.Directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	unlock, err := lockCacheStore(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() { unlock(); file.Close() }, nil
}

func (store CacheStore) Control(request CacheRequest) (map[string]any, error) {
	if request.Action == "list" {
		selections, err := store.List()
		return map[string]any{"selections": selections}, err
	}
	var err error
	switch request.Action {
	case "publish":
		err = store.Publish(request.ID)
	case "release":
		err = store.Release(request.ID)
	default:
		err = errors.New("invalid clipboard cache operation")
	}
	return map[string]any{"ok": err == nil}, err
}

func (store CacheStore) recordPath(id string) (string, error) {
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 16 {
		return "", errors.New("invalid clipboard cache identity")
	}
	return filepath.Join(store.Directory, id+".json"), nil
}

// Register takes ownership only of a directory just returned by Read, Copy,
// Save or LinkCachedFiles. Existing user selections never enter this method.
func (store CacheStore) Register(paths []string) (CachedSelection, error) {
	return store.register(paths, false)
}

func (store CacheStore) register(paths []string, pending bool) (CachedSelection, error) {
	var empty CachedSelection
	if len(paths) == 0 || len(paths) > SelectionLimit {
		return empty, errors.New("invalid clipboard cache selection")
	}
	directory := filepath.Dir(paths[0])
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != directory {
			return empty, errors.New("invalid clipboard cache directory")
		}
	}
	unlock, err := store.lock()
	if err != nil {
		return empty, err
	}
	defer unlock()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return empty, err
	}
	selection := CachedSelection{ID: hex.EncodeToString(token[:]), Paths: append([]string(nil), paths...)}
	marker, err := os.CreateTemp(directory, ".cage-clipboard-owner-")
	if err != nil {
		return empty, err
	}
	_, writeErr := marker.WriteString(selection.ID)
	err = errors.Join(writeErr, marker.Close())
	if err != nil {
		_ = os.Remove(marker.Name())
		return empty, err
	}
	record := cacheRecord{CachedSelection: selection, Directory: directory, Marker: filepath.Base(marker.Name()), Owner: os.Getppid(), Pending: pending, Producer: os.Getpid()}
	if err := store.save(record); err != nil {
		_ = os.Remove(marker.Name())
		return empty, err
	}
	return selection, nil
}

// create registers the new directory before any bytes are copied. A terminated
// producer leaves an exact receipt that the native owner can recover.
func (store CacheStore) create(operation func(func() (string, error)) ([]string, error)) (result CachedSelection, err error) {
	var created CachedSelection
	defer func() {
		if err != nil && created.ID != "" {
			_ = store.Release(created.ID)
		}
	}()
	paths, err := operation(func() (string, error) {
		root, err := temporaryDirectory()
		if err != nil {
			return "", err
		}
		created, err = store.register([]string{filepath.Join(root, ".pending")}, true)
		if err != nil {
			_ = os.RemoveAll(root)
		}
		return root, err
	})
	if err != nil {
		return result, err
	}
	unlock, err := store.lock()
	if err != nil {
		return result, err
	}
	defer unlock()
	record, err := store.load(created.ID)
	if err != nil {
		return result, err
	}
	record.Paths = paths
	record.Pending = false
	if err := store.save(record); err != nil {
		return result, err
	}
	return record.CachedSelection, nil
}

func (store CacheStore) Read(input io.Reader) (CachedSelection, error) {
	return store.create(func(directory func() (string, error)) ([]string, error) { return read(input, directory) })
}

func (store CacheStore) Copy(paths []string) (CachedSelection, error) {
	return store.create(func(directory func() (string, error)) ([]string, error) { return copyFiles(paths, directory) })
}

func (store CacheStore) Save(input io.Reader, name string) (CachedSelection, error) {
	return store.create(func(directory func() (string, error)) ([]string, error) {
		path, err := save(input, name, directory)
		return []string{path}, err
	})
}

func (store CacheStore) LinkCachedFiles(paths []string) (CachedSelection, error) {
	return store.create(func(directory func() (string, error)) ([]string, error) { return linkCachedFiles(paths, directory) })
}

func (store CacheStore) save(record cacheRecord) error {
	path, err := store.recordPath(record.ID)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(store.Directory, ".clipboard-record-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(record); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (store CacheStore) load(id string) (cacheRecord, error) {
	var record cacheRecord
	path, err := store.recordPath(id)
	if err != nil {
		return record, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if json.Unmarshal(data, &record) != nil || record.ID != id || len(record.Paths) == 0 ||
		!filepath.IsAbs(record.Directory) || filepath.Clean(record.Directory) != record.Directory ||
		!validName(record.Marker) || record.Owner < 1 || record.Pending && record.Producer < 1 {
		return record, errors.New("invalid clipboard cache record")
	}
	for _, path := range record.Paths {
		if filepath.Dir(path) != record.Directory || filepath.Clean(path) != path {
			return record, errors.New("invalid clipboard cache paths")
		}
	}
	return record, nil
}

func (store CacheStore) Publish(id string) error {
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	record, err := store.load(id)
	if err != nil {
		return err
	}
	record.Published = true
	return store.save(record)
}

// List recovers published copies and abandoned receives from their creation
// records. A live native owner retains admission for its unpublished transfers.
func (store CacheStore) List() ([]CachedSelection, error) {
	unlock, err := store.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	entries, err := os.ReadDir(store.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return []CachedSelection{}, nil
	}
	if err != nil {
		return nil, err
	}
	selections := []CachedSelection{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := store.load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if record.Pending && !cacheOwnerAlive(record.Producer) || !record.Pending &&
			(record.Published && record.Owner == os.Getppid() || !cacheOwnerAlive(record.Owner)) {
			selections = append(selections, record.CachedSelection)
		}
	}
	return selections, nil
}

// Release runs after the native owner has checked the current OS selection and
// its in-flight readers. A Windows sharing violation keeps the receipt for retry.
func (store CacheStore) Release(id string) error {
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	record, err := store.load(id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	path, _ := store.recordPath(id)
	root, err := os.OpenRoot(record.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return os.Remove(path)
	}
	if err != nil {
		return err
	}
	defer root.Close()
	marker, err := root.ReadFile(record.Marker)
	if err != nil || string(marker) != id {
		return errors.New("clipboard cache creation record no longer matches")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	err = errors.Join(readErr, directory.Close())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != record.Marker {
			if err := root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	if err := removeCacheDirectory(root, record); err != nil {
		return err
	}
	return os.Remove(path)
}

func UserCacheStore() (CacheStore, error) {
	directory, err := os.UserCacheDir()
	return CacheStore{Directory: filepath.Join(directory, "cage", "clipboard-ownership")}, err
}
