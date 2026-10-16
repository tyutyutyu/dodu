package cache

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

// CurrentVersion is the on-disk format version. Bump on incompatible changes.
const CurrentVersion = 2

var bucketSnapshots = []byte("snapshots")

// ErrNotFound means no snapshot exists for the given key.
var ErrNotFound = errors.New("cache: snapshot not found")

// ErrCorrupt means the entry exists but cannot be decoded; callers should
// treat this like a miss and let a fresh scan overwrite it.
var ErrCorrupt = errors.New("cache: corrupt entry")

// BoltCache is a bbolt-backed Cache implementation.
type BoltCache struct {
	db   *bolt.DB
	path string
}

type entry struct {
	Version  int
	SavedAt  time.Time
	Snapshot snapshotPayload
}

// snapshotPayload mirrors scan.Snapshot but with []string for errors so gob
// can roundtrip it.
type snapshotPayload struct {
	LayersSize      int64
	LayersSizeKnown bool
	Daemon          docker.DaemonInfo
	Images          []docker.Image
	Containers      []docker.Container
	Volumes         []docker.Volume
	BuildCache      []docker.BuildCacheEntry
	LogSizes        map[string]int64
	CapturedAt      time.Time
	Duration        time.Duration
	Errors          []string
}

// DefaultPath returns the platform cache path: $XDG_CACHE_HOME/dodu/snapshots.db
// or $HOME/.cache/dodu/snapshots.db.
func DefaultPath() (string, error) {
	if p := os.Getenv("XDG_CACHE_HOME"); p != "" {
		return filepath.Join(p, "dodu", "snapshots.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "dodu", "snapshots.db"), nil
}

// OpenBolt opens (creating if needed) the bbolt cache at path.
func OpenBolt(path string) (*BoltCache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir cache dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(bucketSnapshots)
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init bucket: %w", err)
	}
	return &BoltCache{db: db, path: path}, nil
}

// Path returns the on-disk file path.
func (c *BoltCache) Path() string { return c.path }

// Load returns the snapshot stored for key.
func (c *BoltCache) Load(key string) (*scan.Snapshot, error) {
	var buf []byte
	if err := c.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketSnapshots).Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		buf = append(buf, v...)
		return nil
	}); err != nil {
		return nil, err
	}

	var e entry
	if err := gob.NewDecoder(bytes.NewReader(buf)).Decode(&e); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if e.Version != CurrentVersion {
		return nil, fmt.Errorf("%w: version %d != %d", ErrCorrupt, e.Version, CurrentVersion)
	}

	snap := &scan.Snapshot{
		LayersSize:      e.Snapshot.LayersSize,
		LayersSizeKnown: e.Snapshot.LayersSizeKnown,
		Daemon:          e.Snapshot.Daemon,
		Images:          e.Snapshot.Images,
		Containers:      e.Snapshot.Containers,
		Volumes:         e.Snapshot.Volumes,
		BuildCache:      e.Snapshot.BuildCache,
		LogSizes:        e.Snapshot.LogSizes,
		CapturedAt:      e.Snapshot.CapturedAt,
		Duration:        e.Snapshot.Duration,
	}
	for _, s := range e.Snapshot.Errors {
		snap.Errors = append(snap.Errors, errors.New(s))
	}
	return snap, nil
}

// Save writes the snapshot under key, replacing any prior entry.
func (c *BoltCache) Save(key string, snap *scan.Snapshot) error {
	if snap == nil {
		return errors.New("cache: cannot save nil snapshot")
	}
	payload := snapshotPayload{
		LayersSize:      snap.LayersSize,
		LayersSizeKnown: snap.LayersSizeKnown,
		Daemon:          snap.Daemon,
		Images:          snap.Images,
		Containers:      snap.Containers,
		Volumes:         snap.Volumes,
		BuildCache:      snap.BuildCache,
		LogSizes:        snap.LogSizes,
		CapturedAt:      snap.CapturedAt,
		Duration:        snap.Duration,
	}
	for _, e := range snap.Errors {
		payload.Errors = append(payload.Errors, e.Error())
	}
	e := entry{Version: CurrentVersion, SavedAt: time.Now(), Snapshot: payload}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(e); err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return c.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSnapshots).Put([]byte(key), buf.Bytes())
	})
}

// Purge removes all cached entries.
func (c *BoltCache) Purge() error {
	return c.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketSnapshots); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		_, err := tx.CreateBucket(bucketSnapshots)
		return err
	})
}

// Close releases the underlying bbolt handle.
func (c *BoltCache) Close() error { return c.db.Close() }
