package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Download manifest-referenced image blobs absent locally",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, refs, client, err := syncSetup()
			if err != nil {
				return err
			}
			res := syncResult{Failed: []syncError{}}
			for _, ref := range refs {
				fail := func(err error) {
					res.Failed = append(res.Failed, syncError{SHA256: ref.SHA256, Message: err.Error()})
					fmt.Fprintf(cmd.ErrOrStderr(), "pull: %s: %v\n", ref.SHA256[:12], err)
				}
				var missing []string
				for _, p := range ref.Paths {
					abs := filepath.Join(root, filepath.FromSlash(p))
					if _, err := os.Stat(abs); err == nil {
						res.Skipped++
					} else {
						missing = append(missing, abs)
					}
				}
				if len(missing) == 0 {
					continue
				}
				body, err := client.Get(cmd.Context(), blobKey(ref.SHA256))
				if err != nil {
					fail(err)
					continue
				}
				data, err := io.ReadAll(body)
				body.Close()
				if err != nil {
					fail(err)
					continue
				}
				sum := sha256.Sum256(data)
				if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
					fail(fmt.Errorf("remote blob corrupt: sha256 %s… does not match key", got[:12]))
					continue
				}
				for _, abs := range missing {
					if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
						fail(err)
						break
					}
					// ReplaceFile writes a temp file and renames: a partial
					// download can never land at a manifest-referenced path.
					if err := fsio.ReplaceFile(abs, data); err != nil {
						fail(err)
						break
					}
					res.Downloaded++
				}
			}
			if err := emit(cmd, res, res.human("downloaded")); err != nil {
				return err
			}
			if n := len(res.Failed); n > 0 {
				return fmt.Errorf("pull: %d blob(s) failed", n)
			}
			return nil
		},
	}
}
