package cmd

import (
	"strings"
	"testing"
)

func TestSetNixVersion_basic(t *testing.T) {
	in := `{
  outputs = { ... }: {
    packages.default = pkgs.buildGoApplication {
      pname = "demo";
      version = "0.0.1";
      src = ./.;
    };
  };
}
`
	out, changes, err := setNixVersion([]byte(in), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `version = "1.2.3";`) {
		t.Errorf("version not rewritten:\n%s", out)
	}
	if len(changes) != 1 {
		t.Errorf("got %d changes, want 1: %v", len(changes), changes)
	}
}

func TestSetNixVersion_preservesCommentsAndOrder(t *testing.T) {
	in := `{
  # top-level comment
  outputs = _: {
    pkg = mkDerivation {
      version = "0.5.0"; # bumped by hanko
      pname = "demo";
    };
  };
}
`
	out, _, err := setNixVersion([]byte(in), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# top-level comment") {
		t.Errorf("top comment lost:\n%s", s)
	}
	if !strings.Contains(s, "# bumped by hanko") {
		t.Errorf("trailing comment lost:\n%s", s)
	}
	if !strings.Contains(s, `version = "9.9.9";`) {
		t.Errorf("version not rewritten:\n%s", s)
	}
}

func TestSetNixVersion_multipleMatchingValuesAllReplaced(t *testing.T) {
	// D-015: when a flake has multiple derivations sharing the same version
	// value, hanko rewrites all of them.
	in := `{
  packages.first = mkDerivation {
    pname = "first";
    version = "0.1.0";
  };
  packages.second = mkDerivation {
    pname = "second";
    version = "0.1.0";
  };
}
`
	out, changes, err := setNixVersion([]byte(in), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	count := strings.Count(s, `version = "9.9.9";`)
	if count != 2 {
		t.Errorf("expected 2 version lines rewritten, got %d:\n%s", count, s)
	}
	if strings.Contains(s, `version = "0.1.0";`) {
		t.Errorf("expected no version lines left at old value:\n%s", s)
	}
	if len(changes) != 1 {
		t.Errorf("expected 1 change description (all lines aggregated), got %d: %v", len(changes), changes)
	}
	if !strings.Contains(changes[0], "2 lines") {
		t.Errorf("expected change description to mention 2 lines, got %q", changes[0])
	}
}

func TestSetNixVersion_divergentValuesRefused(t *testing.T) {
	// Two derivations with genuinely different versions → hanko can't pick
	// one without a config hint. Surface to the user.
	in := `{
  packages.first = mkDerivation {
    version = "0.1.0";
  };
  packages.second = mkDerivation {
    version = "0.2.0";
  };
}
`
	_, _, err := setNixVersion([]byte(in), "9.9.9")
	if err == nil {
		t.Fatal("expected error for divergent versions, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "0.1.0") || !strings.Contains(msg, "0.2.0") {
		t.Errorf("error should mention both diverging values, got: %s", msg)
	}
	if !strings.Contains(msg, "inherit version") {
		t.Errorf("error should suggest the shared-`let` pattern, got: %s", msg)
	}
}

func TestSetNixVersion_singleMatchChangeDescription(t *testing.T) {
	// Single-match case still produces a clean description without "(1 line)" noise.
	in := `{
  version = "1.2.3";
}
`
	_, changes, err := setNixVersion([]byte(in), "1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if !strings.Contains(changes[0], "1 line)") {
		t.Errorf("expected description to mention 1 line, got %q", changes[0])
	}
}

func TestSetNixVersion_idempotent(t *testing.T) {
	in := `{
  version = "1.2.3";
}
`
	out, _, err := setNixVersion([]byte(in), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("expected no-op when version unchanged:\nin:  %q\nout: %q", in, out)
	}
}

func TestSetNixVersion_errorsIfMissing(t *testing.T) {
	in := `{
  pname = "demo";
}
`
	_, _, err := setNixVersion([]byte(in), "1.0.0")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestSetNixVersion_skipsNonStringVersionLines(t *testing.T) {
	// A `version` attr whose value isn't a string literal (function call,
	// let-binding ref, etc.) shouldn't match — the regex requires `"..."`.
	in := `{
  version = pkgs.lib.fileContents ./VERSION;
  package = mkDerivation {
    version = "0.1.0";
  };
}
`
	out, _, err := setNixVersion([]byte(in), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `version = pkgs.lib.fileContents`) {
		t.Errorf("function-valued version was altered:\n%s", s)
	}
	if !strings.Contains(s, `version = "9.9.9";`) {
		t.Errorf("string-valued version not rewritten:\n%s", s)
	}
}
