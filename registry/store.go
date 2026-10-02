package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/shikejing/schema-registry-go/schema"
)

// snapshotVersion is the on-disk format version, allowing future migrations.
const snapshotVersion = 1

// snapshot is the persisted on-disk state of the registry. Only fully
// committed data is ever written, so an interrupted registration leaves no
// intermediate state behind.
type snapshot struct {
	FormatVersion int                     `json:"formatVersion"`
	Subjects      map[string]subjectState `json:"subjects"`
}

type subjectState struct {
	Versions []*schema.Version    `json:"versions"`
	Config   schema.SubjectConfig `json:"config"`
}

func newSnapshot() *snapshot {
	return &snapshot{FormatVersion: snapshotVersion, Subjects: map[string]subjectState{}}
}

// loadSnapshot reads and parses the snapshot file, returning an empty snapshot
// when the file does not exist yet.
func loadSnapshot(path string) (*snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newSnapshot(), nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	snap := newSnapshot()
	if len(b) == 0 {
		return snap, nil
	}
	if err := json.Unmarshal(b, snap); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	if snap.FormatVersion != snapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot format version %d", snap.FormatVersion)
	}
	if snap.Subjects == nil {
		snap.Subjects = map[string]subjectState{}
	}
	return snap, nil
}

// saveSnapshot atomically writes the snapshot to path: data is flushed and
// fsynced to a sibling temporary file, which is then renamed over the target.
// A crash at any point therefore leaves either the old or the new snapshot,
// never a torn file.
func saveSnapshot(path string, snap *snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp snapshot: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("commit snapshot: %w", err)
	}
	return nil
}
