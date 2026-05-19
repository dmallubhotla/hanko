package version

import (
	"testing"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/dmallubhotla/hanko/internal/gitinfo"
)

func TestCompute(t *testing.T) {
	cases := []struct {
		name       string
		info       gitinfo.Info
		wantSemVer string
		wantFull   string
		wantPre    bool
	}{
		{
			name: "mainline no commits since tag",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "v1.2.3",
				CommitsSinceTag: 0, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.3",
			wantFull:   "1.2.3+0.abc1234",
		},
		{
			name: "mainline with commits since tag",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "v1.2.3",
				CommitsSinceTag: 5, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.8",
			wantFull:   "1.2.8+5.abc1234",
		},
		{
			name: "master is mainline too",
			info: gitinfo.Info{
				Branch: "master", LatestTag: "v0.5.0",
				CommitsSinceTag: 2, ShortSha: "abc1234",
			},
			wantSemVer: "0.5.2",
			wantFull:   "0.5.2+2.abc1234",
		},
		{
			name: "release branch x.y bumps patch",
			info: gitinfo.Info{
				Branch: "release/2.3", LatestTag: "v2.3.0",
				CommitsSinceTag: 4, ShortSha: "abc1234",
			},
			wantSemVer: "2.3.4",
			wantFull:   "2.3.4+4.abc1234",
		},
		{
			name: "hotfix branch is pre-release of next patch",
			info: gitinfo.Info{
				Branch: "hotfix/urgent", LatestTag: "v1.2.3",
				CommitsSinceTag: 2, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.4-hotfix.2",
			wantFull:   "1.2.4-hotfix.2+2.abc1234",
			wantPre:    true,
		},
		{
			name: "feature branch is pre-release at base",
			info: gitinfo.Info{
				Branch: "feature/foo", LatestTag: "v1.2.3",
				CommitsSinceTag: 3, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.3-feature-foo.3",
			wantFull:   "1.2.3-feature-foo.3+3.abc1234",
			wantPre:    true,
		},
		{
			name: "ticket branch sanitised",
			info: gitinfo.Info{
				Branch: "HHENG-568", LatestTag: "v1.0.0",
				CommitsSinceTag: 1, ShortSha: "abc1234",
			},
			wantSemVer: "1.0.0-hheng-568.1",
			wantFull:   "1.0.0-hheng-568.1+1.abc1234",
			wantPre:    true,
		},
		{
			name: "no tag in repo gives 0.1.0 prerelease",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "",
				CommitsSinceTag: 2, ShortSha: "abc1234",
			},
			wantSemVer: "0.1.0-main.2",
			wantFull:   "0.1.0-main.2+2.abc1234",
			wantPre:    true,
		},
		{
			name: "dirty appends to build metadata",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "v1.0.0",
				CommitsSinceTag: 0, ShortSha: "abc1234", Dirty: true,
			},
			wantSemVer: "1.0.0",
			wantFull:   "1.0.0+0.abc1234.dirty",
		},
		{
			name: "bare semver tag without v prefix",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "1.2.3",
				CommitsSinceTag: 0, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.3",
			wantFull:   "1.2.3+0.abc1234",
		},
		{
			name: "detached HEAD with no branch falls back to sentinel",
			info: gitinfo.Info{
				Branch: "", Detached: true, LatestTag: "v1.0.0",
				CommitsSinceTag: 1, ShortSha: "abc1234",
			},
			wantSemVer: "1.0.0-detached.1",
			wantFull:   "1.0.0-detached.1+1.abc1234",
			wantPre:    true,
		},
		{
			name: "detached HEAD AT a release tag → emit tag verbatim (D-001)",
			info: gitinfo.Info{
				Branch: "", Detached: true, LatestTag: "v1.2.3",
				CommitsSinceTag: 0, ShortSha: "abc1234", CommitDate: "2026-01-01T00:00:00Z",
			},
			wantSemVer: "1.2.3",
			wantFull:   "1.2.3",
		},
		{
			name: "detached HEAD at a prerelease tag → emit prerelease verbatim (D-001)",
			info: gitinfo.Info{
				Branch: "", Detached: true, LatestTag: "v1.2.3-rc.1",
				CommitsSinceTag: 0, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.3-rc.1",
			wantFull:   "1.2.3-rc.1",
			wantPre:    true,
		},
		{
			name: "detached HEAD at a dirty tag → emit tag + dirty suffix (D-001)",
			info: gitinfo.Info{
				Branch: "", Detached: true, LatestTag: "v1.2.3",
				CommitsSinceTag: 0, ShortSha: "abc1234", Dirty: true,
			},
			wantSemVer: "1.2.3",
			wantFull:   "1.2.3+dirty",
		},
		{
			name: "non-detached at a tag (mainline policy) → plain release",
			info: gitinfo.Info{
				Branch: "main", LatestTag: "v1.2.3",
				CommitsSinceTag: 0, ShortSha: "abc1234",
			},
			wantSemVer: "1.2.3",
			wantFull:   "1.2.3+0.abc1234",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Compute(tc.info, config.Defaults())
			if err != nil {
				t.Fatal(err)
			}
			if v.SemVer != tc.wantSemVer {
				t.Errorf("SemVer = %q, want %q", v.SemVer, tc.wantSemVer)
			}
			if v.FullSemVer != tc.wantFull {
				t.Errorf("FullSemVer = %q, want %q", v.FullSemVer, tc.wantFull)
			}
			if v.IsPreRelease != tc.wantPre {
				t.Errorf("IsPreRelease = %v, want %v", v.IsPreRelease, tc.wantPre)
			}
		})
	}
}

func TestSanitizeBranch(t *testing.T) {
	cases := map[string]string{
		"feature/foo":     "feature-foo",
		"feature/FOO_bar": "feature-foo-bar",
		"HHENG-568":       "hheng-568",
		"main":            "main",
		"---weird---":     "weird",
		"":                "branch",
	}
	for in, want := range cases {
		if got := sanitizeBranch(in); got != want {
			t.Errorf("sanitizeBranch(%q) = %q, want %q", in, got, want)
		}
	}
}
