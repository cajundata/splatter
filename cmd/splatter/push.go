package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/spaces"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// syncError is one blob that could not be synced.
type syncError struct {
	SHA256  string `json:"sha256"`
	Message string `json:"message"`
}

// syncResult is the shared push/pull result object (spec: one struct,
// direction-specific fields zero for the other command).
type syncResult struct {
	Uploaded     int         `json:"uploaded"`
	Downloaded   int         `json:"downloaded"`
	Skipped      int         `json:"skipped"`
	MissingLocal int         `json:"missing_local"`
	Failed       []syncError `json:"failed"`
}

func (r *syncResult) human(verb string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d\n", verb, r.Uploaded+r.Downloaded)
	fmt.Fprintf(&b, "skipped: %d\n", r.Skipped)
	if r.MissingLocal > 0 {
		fmt.Fprintf(&b, "missing locally: %d\n", r.MissingLocal)
	}
	fmt.Fprintf(&b, "failed: %d\n", len(r.Failed))
	return b.String()
}

// blobKey maps an image sha to its flat content-addressed key (spec §7).
func blobKey(sha string) string { return "blobs/" + sha }

// syncSetup is the shared push/pull preamble: workspace root, config,
// blob inventory, client.
func syncSetup() (root string, refs []workspace.BlobRef, client *spaces.Client, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, nil, err
	}
	root, err = workspace.FindRoot(cwd)
	if err != nil {
		if errors.Is(err, workspace.ErrNoWorkspace) {
			return "", nil, nil, usageErr{err}
		}
		return "", nil, nil, err
	}
	cfg, err := spaces.FromEnv()
	if err != nil {
		return "", nil, nil, err // runtime error: matches missing-API-key behavior
	}
	refs, err = workspace.BlobRefs(root)
	if err != nil {
		return "", nil, nil, err
	}
	return root, refs, spaces.New(cfg), nil
}

func newPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Upload manifest-referenced image blobs absent from Spaces",
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
					fmt.Fprintf(cmd.ErrOrStderr(), "push: %s: %v\n", ref.SHA256[:12], err)
				}
				var local string
				for _, p := range ref.Paths {
					abs := filepath.Join(root, filepath.FromSlash(p))
					if _, err := os.Stat(abs); err == nil {
						local = abs
						break
					}
				}
				if local == "" {
					res.MissingLocal++
					continue
				}
				data, err := os.ReadFile(local)
				if err != nil {
					fail(err)
					continue
				}
				sum := sha256.Sum256(data)
				if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
					fail(fmt.Errorf("%s: sha256 %s… does not match manifest — refusing to upload", local, got[:12]))
					continue
				}
				exists, err := client.Head(cmd.Context(), blobKey(ref.SHA256))
				if err != nil {
					fail(err)
					continue
				}
				if exists {
					res.Skipped++
					continue
				}
				if err := client.Put(cmd.Context(), blobKey(ref.SHA256), data, ref.SHA256); err != nil {
					fail(err)
					continue
				}
				res.Uploaded++
			}
			if err := emit(cmd, res, res.human("uploaded")); err != nil {
				return err
			}
			if n := len(res.Failed); n > 0 {
				return fmt.Errorf("push: %d blob(s) failed", n)
			}
			return nil
		},
	}
}
