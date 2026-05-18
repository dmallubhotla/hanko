package cmd

import (
	"strings"
	"testing"
)

func TestSetChartVersions_quotedAppVersion(t *testing.T) {
	in := `apiVersion: v2
name: demo
version: 0.0.0
appVersion: "0.0.0"
`
	out, changes, err := setChartVersions([]byte(in), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: v2
name: demo
version: 1.2.3
appVersion: "1.2.3"
`
	if string(out) != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	if len(changes) != 2 {
		t.Errorf("got %d changes, want 2: %v", len(changes), changes)
	}
}

func TestSetChartVersions_preservesCommentsAndOrder(t *testing.T) {
	in := `# top comment
apiVersion: v2
appVersion: "0.5.0"   # bumped by hanko
name: demo
version: 0.5.0  # also bumped
type: application
`
	out, _, err := setChartVersions([]byte(in), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `# bumped by hanko`) {
		t.Errorf("trailing comment was lost:\n%s", out)
	}
	if !strings.Contains(string(out), `# top comment`) {
		t.Errorf("top comment was lost")
	}
	if !strings.Contains(string(out), `appVersion: "9.9.9"`) {
		t.Errorf("appVersion not rewritten:\n%s", out)
	}
	if !strings.Contains(string(out), `version: 9.9.9`) {
		t.Errorf("version not rewritten:\n%s", out)
	}
}

func TestSetChartVersions_errorsIfMissingKey(t *testing.T) {
	cases := map[string]string{
		"missing version":    "apiVersion: v2\nname: demo\nappVersion: \"0.0.0\"\n",
		"missing appVersion": "apiVersion: v2\nname: demo\nversion: 0.0.0\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := setChartVersions([]byte(in), "1.0.0")
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestSetChartVersions_idempotent(t *testing.T) {
	in := "apiVersion: v2\nversion: 1.2.3\nappVersion: \"1.2.3\"\n"
	out1, _, err := setChartVersions([]byte(in), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != in {
		t.Errorf("expected no-op when version unchanged:\nin:  %q\nout: %q", in, out1)
	}
}
