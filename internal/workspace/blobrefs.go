package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/cajundata/splatter/internal/schema"
)

// BlobRef is one content-addressed image blob and every workspace-
// relative, slash-normalized path that references it. Multiple paths
// occur when several manifests record the same bytes.
type BlobRef struct {
	SHA256 string
	Paths  []string
}

// BlobRefs scans every project's manifests and returns the blob
// inventory sorted by sha (paths sorted within each ref). Malformed
// manifest lines are errors: sync must never silently skip evidence.
func BlobRefs(root string) ([]BlobRef, error) {
	names, err := projectNames(root, "")
	if err != nil {
		return nil, err
	}
	bySHA := map[string]map[string]bool{}
	for _, name := range names {
		if err := collectProjectBlobs(root, name, bySHA); err != nil {
			return nil, err
		}
	}
	return flattenBlobRefs(bySHA), nil
}

// projectBlobRefs is BlobRefs for a single project (status uses it).
func projectBlobRefs(root, name string) ([]BlobRef, error) {
	bySHA := map[string]map[string]bool{}
	if err := collectProjectBlobs(root, name, bySHA); err != nil {
		return nil, err
	}
	return flattenBlobRefs(bySHA), nil
}

func collectProjectBlobs(root, name string, bySHA map[string]map[string]bool) error {
	runDirs, _ := filepath.Glob(filepath.Join(root, "projects", name, "runs", "r_*"))
	for _, rd := range runDirs {
		if fi, err := os.Stat(rd); err != nil || !fi.IsDir() {
			continue // a stray file matching r_* is not a run; validate flags this
		}
		mp := filepath.Join(rd, "manifest.jsonl")
		f, err := os.Open(mp)
		if os.IsNotExist(err) {
			continue // validate flags this; sync syncs what exists
		}
		if err != nil {
			return err
		}
		relRun, err := filepath.Rel(root, rd)
		if err != nil {
			f.Close()
			return err
		}
		runPrefix := filepath.ToSlash(relRun)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			decoded, err := schema.DecodeManifestLine(sc.Bytes())
			if err != nil {
				f.Close()
				return fmt.Errorf("%s line %d: %w", mp, lineNo, err)
			}
			rec, ok := decoded.(*schema.CallRecord)
			if !ok {
				continue
			}
			for _, img := range rec.Images {
				p := path.Join(runPrefix, img.File)
				if bySHA[img.SHA256] == nil {
					bySHA[img.SHA256] = map[string]bool{}
				}
				bySHA[img.SHA256][p] = true
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return fmt.Errorf("%s: %w", mp, err)
		}
	}
	return nil
}

func flattenBlobRefs(bySHA map[string]map[string]bool) []BlobRef {
	refs := make([]BlobRef, 0, len(bySHA))
	for sha, paths := range bySHA {
		ref := BlobRef{SHA256: sha}
		for p := range paths {
			ref.Paths = append(ref.Paths, p)
		}
		sort.Strings(ref.Paths)
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].SHA256 < refs[j].SHA256 })
	return refs
}
