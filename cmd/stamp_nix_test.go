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

func TestSetNixVersion_firstMatchWins(t *testing.T) {
	in := `{
  packages.first = mkDerivation {
    pname = "first";
    version = "0.1.0";
  };
  packages.second = mkDerivation {
    pname = "second";
    version = "0.2.0";
  };
}
`
	out, _, err := setNixVersion([]byte(in), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `version = "9.9.9";`) {
		t.Errorf("expected first version rewritten:\n%s", s)
	}
	if !strings.Contains(s, `version = "0.2.0";`) {
		t.Errorf("expected second version untouched:\n%s", s)
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
