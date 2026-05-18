package gitinfo

import (
	"errors"
	"testing"

	"github.com/dmallubhotla/hanko/internal/testrepo"
)

func TestRead_emptyRepoReturnsErrNoCommits(t *testing.T) {
	r := testrepo.New(t)
	_, err := Read(r.Dir())
	if !errors.Is(err, ErrNoCommits) {
		t.Fatalf("want ErrNoCommits, got %v", err)
	}
}

func TestRead_singleCommitNoTag(t *testing.T) {
	r := testrepo.New(t).Commit("initial")
	info, err := Read(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "main" {
		t.Errorf("Branch = %q, want main", info.Branch)
	}
	if info.LatestTag != "" {
		t.Errorf("LatestTag = %q, want empty", info.LatestTag)
	}
	if info.CommitsSinceTag != 1 {
		t.Errorf("CommitsSinceTag = %d, want 1 (total commits from root)", info.CommitsSinceTag)
	}
	if info.Dirty {
		t.Errorf("Dirty = true, want false")
	}
	if info.Detached {
		t.Errorf("Detached = true, want false")
	}
	if info.Shallow {
		t.Errorf("Shallow = true, want false")
	}
	if info.Sha == "" {
		t.Errorf("Sha should not be empty")
	}
	if info.ShortSha == "" {
		t.Errorf("ShortSha should not be empty")
	}
	if info.CommitDate == "" {
		t.Errorf("CommitDate should not be empty")
	}
}

func TestRead_tagAndCommitsSince(t *testing.T) {
	r := testrepo.New(t).
		Commit("one").
		Tag("v1.2.3").
		Commit("two").
		Commit("three")
	info, err := Read(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestTag != "v1.2.3" {
		t.Errorf("LatestTag = %q, want v1.2.3", info.LatestTag)
	}
	if info.CommitsSinceTag != 2 {
		t.Errorf("CommitsSinceTag = %d, want 2", info.CommitsSinceTag)
	}
}

func TestRead_dirtyWorktree(t *testing.T) {
	r := testrepo.New(t).Commit("one").WriteFile("untracked", "hi")
	info, err := Read(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Dirty {
		t.Errorf("Dirty = false, want true (untracked file present)")
	}
}

func TestRead_detachedHead(t *testing.T) {
	r := testrepo.New(t).Commit("one").Commit("two")
	r.Git("checkout", "-q", "HEAD~1")
	info, err := Read(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detached {
		t.Errorf("Detached = false, want true")
	}
	if info.Branch != "" {
		t.Errorf("Branch = %q, want empty on detached HEAD", info.Branch)
	}
}

func TestRead_featureBranch(t *testing.T) {
	r := testrepo.New(t).Commit("one").Tag("v1.0.0").Checkout("feature/foo").Commit("two")
	info, err := Read(r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "feature/foo" {
		t.Errorf("Branch = %q, want feature/foo", info.Branch)
	}
	if info.LatestTag != "v1.0.0" {
		t.Errorf("LatestTag = %q, want v1.0.0", info.LatestTag)
	}
	if info.CommitsSinceTag != 1 {
		t.Errorf("CommitsSinceTag = %d, want 1", info.CommitsSinceTag)
	}
}
