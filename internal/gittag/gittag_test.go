package gittag

import (
	"errors"
	"strings"
	"testing"

	"github.com/dmallubhotla/hanko/internal/testrepo"
)

func TestEnsureAtHead_createsAnnotatedTag(t *testing.T) {
	r := testrepo.New(t).Commit("one")
	already, err := EnsureAtHead(r.Dir(), "v1.0.0", "Release v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if already {
		t.Errorf("alreadyExists = true, want false")
	}

	// Confirm the tag exists and is annotated (has a tag object, not just a ref).
	out := r.Git("cat-file", "-t", "v1.0.0")
	if strings.TrimSpace(out) != "tag" {
		t.Errorf("cat-file -t v1.0.0 = %q, want %q (annotated tag)", strings.TrimSpace(out), "tag")
	}
}

func TestEnsureAtHead_idempotentWhenAlreadyAtHead(t *testing.T) {
	r := testrepo.New(t).Commit("one").Tag("v1.0.0")
	already, err := EnsureAtHead(r.Dir(), "v1.0.0", "msg", false)
	if err != nil {
		t.Fatalf("expected no error on idempotent rerun, got %v", err)
	}
	if !already {
		t.Errorf("alreadyExists = false, want true")
	}
}

func TestEnsureAtHead_conflictWhenTagPointsElsewhere(t *testing.T) {
	r := testrepo.New(t).Commit("one").Tag("v1.0.0").Commit("two")
	_, err := EnsureAtHead(r.Dir(), "v1.0.0", "msg", false)
	if !errors.Is(err, ErrTagConflict) {
		t.Fatalf("want ErrTagConflict, got %v", err)
	}
}

func TestExists_andAtHead(t *testing.T) {
	r := testrepo.New(t).Commit("one").Tag("v1.0.0").Commit("two")

	ok, err := Exists(r.Dir(), "v1.0.0")
	if err != nil || !ok {
		t.Errorf("Exists(v1.0.0) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = Exists(r.Dir(), "v9.9.9")
	if err != nil || ok {
		t.Errorf("Exists(v9.9.9) = (%v, %v), want (false, nil)", ok, err)
	}

	ok, err = AtHead(r.Dir(), "v1.0.0")
	if err != nil || ok {
		t.Errorf("AtHead(v1.0.0) = (%v, %v), want (false, nil) — tag is on parent", ok, err)
	}
}
