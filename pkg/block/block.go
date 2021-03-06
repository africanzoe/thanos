// Package block contains common functionality for interacting with TSDB blocks
// in the context of Thanos.
package block

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"context"
	"path"

	"github.com/improbable-eng/thanos/pkg/objstore"
	"github.com/oklog/ulid"
	"github.com/pkg/errors"
	"github.com/prometheus/tsdb"
	"github.com/prometheus/tsdb/fileutil"
)

// Meta describes the a block's meta. It wraps the known TSDB meta structure and
// extends it by Thanos-specific fields.
type Meta struct {
	Version int `json:"version"`

	tsdb.BlockMeta

	Thanos ThanosMeta `json:"thanos"`
}

// ThanosMeta holds block meta information specific to Thanos.
type ThanosMeta struct {
	Labels     map[string]string `json:"labels"`
	Downsample struct {
		Resolution int64 `json:"resolution"`
	} `json:"downsample"`
}

// MetaFilename is the known JSON filename for meta information.
const MetaFilename = "meta.json"

// WriteMetaFile writes the given meta into <dir>/meta.json.
func WriteMetaFile(dir string, meta *Meta) error {
	// Make any changes to the file appear atomic.
	path := filepath.Join(dir, MetaFilename)
	tmp := path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "\t")

	if err := enc.Encode(meta); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return renameFile(tmp, path)
}

// ReadMetaFile reads the given meta from <dir>/meta.json.
func ReadMetaFile(dir string) (*Meta, error) {
	b, err := ioutil.ReadFile(filepath.Join(dir, MetaFilename))
	if err != nil {
		return nil, err
	}
	var m Meta

	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Version != 1 {
		return nil, errors.Errorf("unexpected meta file version %d", m.Version)
	}
	return &m, nil
}

func renameFile(from, to string) error {
	if err := os.RemoveAll(to); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}

	// Directory was renamed; sync parent dir to persist rename.
	pdir, err := fileutil.OpenDir(filepath.Dir(to))
	if err != nil {
		return err
	}

	if err = fileutil.Fsync(pdir); err != nil {
		pdir.Close()
		return err
	}
	return pdir.Close()
}

// Download downloads directory that is mean to be block directory.
func Download(ctx context.Context, bucket objstore.Bucket, id ulid.ULID, dst string) error {
	if err := objstore.DownloadDir(ctx, bucket, id.String(), dst); err != nil {
		return err
	}

	chunksDir := filepath.Join(dst, "chunks")
	_, err := os.Stat(chunksDir)
	if os.IsNotExist(err) {
		// This can happen if block is empty. We cannot easily upload empty directory, so create one here.
		return os.Mkdir(chunksDir, os.ModePerm)
	}

	if err != nil {
		return errors.Wrapf(err, "stat %s", chunksDir)
	}

	return nil
}

// Delete removes directory that is mean to be block directory.
// NOTE: Prefer this method instead of objstore.Delete to avoid deleting empty dir (whole bucket) by mistake.
func Delete(ctx context.Context, bucket objstore.Bucket, id ulid.ULID) error {
	return objstore.DeleteDir(ctx, bucket, id.String())
}

// DownloadMeta downloads only meta file from bucket by block ID.
func DownloadMeta(ctx context.Context, bkt objstore.Bucket, id ulid.ULID) (Meta, error) {
	rc, err := bkt.Get(ctx, path.Join(id.String(), "meta.json"))
	if err != nil {
		return Meta{}, errors.Wrapf(err, "meta.json bkt get for %s", id.String())
	}
	defer rc.Close()

	var m Meta
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return Meta{}, errors.Wrapf(err, "decode meta.json for block %s", id.String())
	}
	return m, nil
}

func IsBlockDir(path string) (id ulid.ULID, ok bool) {
	id, err := ulid.Parse(filepath.Base(path))
	return id, err == nil
}
