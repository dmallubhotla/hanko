package gitinfo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dmallubhotla/hanko/internal/testrepo"
)

// touchInGitDir writes an empty marker file/dir at the named path inside the
// repo's .git directory. Used to simulate in-progress git operations without
// actually triggering them (which would require interactive input or merge
// conflicts).
func touchInGitDir(t *testing.T, repo, relPath string, isDir bool) {
	t.Helper()
	gitDir := filepath.Join(repo, ".git")
	full := filepath.Join(gitDir, relPath)
	if isDir {
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead_noOperationInProgress(t *testing.T) {
	r := testrepo.New(t).Commit("initial")
	info, err := Read(r.Dir(), defaultGlobs())
	if err != nil {
		t.Fatal(err)
	}
	if info.InProgress != "" {
		t.Errorf("InProgress = %q, want empty", info.InProgress)
	}
}

func TestRead_detectsInProgressStates(t *testing.T) {
	cases := []struct {
		name, marker string
		isDir        bool
		want         string
	}{
		{"merge", "MERGE_HEAD", false, "merge"},
		{"cherry-pick", "CHERRY_PICK_HEAD", false, "cherry-pick"},
		{"revert", "REVERT_HEAD", false, "revert"},
		{"rebase-merge", "rebase-merge", true, "rebase"},
		{"rebase-apply", "rebase-apply", true, "rebase"},
		{"bisect", "BISECT_LOG", false, "bisect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := testrepo.New(t).Commit("initial")
			touchInGitDir(t, r.Dir(), tc.marker, tc.isDir)
			info, err := Read(r.Dir(), defaultGlobs())
			if err != nil {
				t.Fatal(err)
			}
			if info.InProgress != tc.want {
				t.Errorf("InProgress = %q, want %q", info.InProgress, tc.want)
			}
		})
	}
}

func TestRead_mergeWinsWhenMultipleMarkers(t *testing.T) {
	// Edge case: both MERGE_HEAD and BISECT_LOG present (unlikely in reality,
	// but documents the "first match wins" detection order).
	r := testrepo.New(t).Commit("initial")
	touchInGitDir(t, r.Dir(), "MERGE_HEAD", false)
	touchInGitDir(t, r.Dir(), "BISECT_LOG", false)
	info, err := Read(r.Dir(), defaultGlobs())
	if err != nil {
		t.Fatal(err)
	}
	if info.InProgress != "merge" {
		t.Errorf("InProgress = %q, want merge (first-match-wins)", info.InProgress)
	}
}
