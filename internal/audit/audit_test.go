// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

package audit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YakDriver/hedgetrimmer/internal/config"
)

func testConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.hcl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const testHCL = `
identities = { "jared@example.com" = "jar-b" }
bot_emails = ["changelogbot@"]
protect "releases" {
  title    = "Release branches"
  patterns = ["^release", "^v[0-9]"]
}
protect "pages" { patterns = ["^gh-pages$"] }
`

var roster = map[string]bool{"gdavison": true, "jar-b": true}

func commitBy(email, login string) commit {
	var c commit
	c.Author.Email, c.Author.User.Login = email, login
	return c
}

func TestOwner(t *testing.T) {
	cfg := testConfig(t, testHCL)
	tests := []struct {
		name       string
		pr         *PR
		c          commit
		wantStatus string
		wantWho    string
	}{
		{"maintainer via PR author", &PR{Author: "GDavison"}, commitBy("x@example.com", ""), StatusMaintainer, "GDavison"},
		{"maintainer via linked commit", nil, commitBy("", "jar-b"), StatusMaintainer, "jar-b"},
		{"maintainer via identities", nil, commitBy("Jared@Example.com", ""), StatusMaintainer, "jar-b"},
		{"external", &PR{Author: "mattbork"}, commit{}, StatusOther, "mattbork"},
		{"github app PR", &PR{Author: "app/copilot-swe-agent", Bot: true}, commit{}, StatusBot, "app/copilot-swe-agent"},
		{"bot committer default marker", nil, commitBy("41898282+github-actions[bot]@users.noreply.github.com", ""), StatusBot, "bot"},
		{"bot committer configured marker", nil, commitBy("changelogbot@example.com", ""), StatusBot, "bot"},
		{"unlinked email", nil, commitBy("nobody@nowhere.local", ""), StatusUnknown, "?"},
		{"maintainer beats bot", &PR{Author: "gdavison"}, commitBy("x[bot]@example.com", ""), StatusMaintainer, "gdavison"},
		{"bot beats external", &PR{Author: "app/copilot-swe-agent", Bot: true}, commitBy("198982749+Copilot@users.noreply.github.com", "Copilot"), StatusBot, "app/copilot-swe-agent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, who := owner(cfg, roster, tc.pr, tc.c)
			if st != tc.wantStatus || who != tc.wantWho {
				t.Errorf("owner() = (%q, %q), want (%q, %q)", st, who, tc.wantStatus, tc.wantWho)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	cfg := testConfig(t, testHCL)
	open := &PR{State: "OPEN"}
	tests := []struct {
		name string
		b    Branch
		want string
	}{
		{"pages protected", Branch{Name: "gh-pages", PR: open}, "pages"},
		{"release protected over open PR", Branch{Name: "release/6.x", PR: open}, "releases"},
		{"v protected", Branch{Name: "v6.0.0-upgrade", InBase: true}, "releases"},
		{"open PR", Branch{Name: "f-x", PR: open, Status: StatusBot}, "R6"},
		{"bot", Branch{Name: "compliance/x", PR: &PR{State: "CLOSED"}, Status: StatusBot}, "R1"},
		{"in base", Branch{Name: "f-merged", InBase: true, Status: StatusMaintainer, Age: 900}, "R2"},
		{"other", Branch{Name: "f-old", Status: StatusOther}, "R3"},
		{"unknown", Branch{Name: "f-old", Status: StatusUnknown}, "R3"},
		{"stale", Branch{Name: "f-stale", Status: StatusMaintainer, Age: 366}, "R4"},
		{"boundary is recent", Branch{Name: "f-edge", Status: StatusMaintainer, Age: 365}, "R5"},
		{"recent", Branch{Name: "f-new", Status: StatusMaintainer, Age: 3}, "R5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			classify(cfg, &tc.b)
			if tc.b.Bucket != tc.want {
				t.Errorf("bucket = %q, want %q", tc.b.Bucket, tc.want)
			}
			if tc.b.Protects != (tc.want == "pages" || tc.want == "releases") {
				t.Errorf("Protects = %v", tc.b.Protects)
			}
		})
	}
}

func TestParseRefsPage(t *testing.T) {
	data := []byte(`{"data":{"repository":{
	  "defaultBranchRef":{"name":"main","target":{"oid":"base1"}},
	  "refs":{"pageInfo":{"hasNextPage":true,"endCursor":"abc"},"nodes":[
	    {"name":"open-behind-closed","target":{"oid":"1111","committedDate":"2026-11-01T00:00:00Z",
	      "author":{"email":"g@example.com","user":{"login":"gdavison"}}},
	     "openPRs":{"nodes":[{"number":5,"state":"OPEN","author":{"login":"copilot-swe-agent","__typename":"Bot"}}]},
	     "recentPRs":{"nodes":[
	       {"number":19,"state":"CLOSED","author":{"login":"gdavison","__typename":"User"}},
	       {"number":18,"state":"CLOSED","author":{"login":"gdavison","__typename":"User"}}]}},
	    {"name":"two-closed","target":{"oid":"3333","committedDate":"2026-01-01T00:00:00Z","author":{"email":""}},
	     "openPRs":{"nodes":[]},
	     "recentPRs":{"nodes":[
	       {"number":49078,"state":"MERGED","author":null},
	       {"number":49079,"state":"CLOSED","author":null}]}},
	    {"name":"no-pr","target":{"oid":"2222","committedDate":"2026-01-01T00:00:00Z",
	      "author":{"email":"x@example.com","user":null}},
	     "openPRs":{"nodes":[]},"recentPRs":{"nodes":[]}}]}}}}`)
	page, err := parseRefsPage(data)
	if err != nil {
		t.Fatal(err)
	}
	r := page.Data.Repository
	if r.DefaultBranchRef.Name != "main" || r.DefaultBranchRef.Target.OID != "base1" ||
		!r.Refs.PageInfo.HasNextPage || r.Refs.PageInfo.EndCursor != "abc" || len(r.Refs.Nodes) != 3 {
		t.Fatalf("unexpected page: %+v", r)
	}

	// An open PR wins even when the recent list (capped at 10) doesn't
	// include it.
	pr := r.Refs.Nodes[0].pr()
	if pr.Number != 5 || pr.State != "OPEN" || !pr.Bot || pr.Author != "app/copilot-swe-agent" {
		t.Errorf("want open PR #5, got %+v", pr)
	}
	if pr := r.Refs.Nodes[1].pr(); pr.Number != 49079 || pr.Author != "" {
		t.Errorf("want highest-numbered PR with no author, got %+v", pr)
	}
	if r.Refs.Nodes[2].pr() != nil {
		t.Error("want nil PR")
	}

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	b := r.Refs.Nodes[2].branch(testConfig(t, ""), roster, now)
	if b.Age != 326 || b.Status != StatusUnknown || b.SHA != "2222" {
		t.Errorf("branch = %+v", b)
	}

	if _, err := parseRefsPage([]byte(`{"data":{"repository":null}}`)); err == nil {
		t.Error("want error for inaccessible repo")
	}
}

func TestRefsArgs_rawStrings(t *testing.T) {
	args := refsArgs("2026", "true", "null")
	for i, a := range args {
		if a == "-F" {
			t.Errorf("args[%d] is -F, which type-converts values: %q", i, args)
		}
	}
	for _, want := range []string{"owner=2026", "name=true", "cursor=null"} {
		if i := slices.Index(args, want); i < 1 || args[i-1] != "-f" {
			t.Errorf("%q not passed with -f: %q", want, args)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestBranchInBase(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "a", "a")

	// "picked" holds one commit that main later cherry-picks.
	git(t, dir, "switch", "-q", "-c", "picked")
	picked := commitFile(t, dir, "b", "b")
	git(t, dir, "switch", "-q", "main")
	git(t, dir, "cherry-pick", picked)

	// "unique" has a commit main lacks.
	git(t, dir, "switch", "-q", "-c", "unique")
	unique := commitFile(t, dir, "c", "c")

	// "merged" is an evil merge: its only ordinary commit is upstream, but
	// merging main into it also adds a file that exists nowhere else.
	git(t, dir, "switch", "-q", "main")
	commitFile(t, dir, "d", "d")
	git(t, dir, "switch", "-q", "-c", "merged", picked)
	git(t, dir, "merge", "-q", "--no-commit", "--no-ff", "main")
	if err := os.WriteFile(filepath.Join(dir, "evil"), []byte("only here"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "evil")
	git(t, dir, "commit", "-q", "--no-edit")
	merged := git(t, dir, "rev-parse", "HEAD")
	if out := git(t, dir, "cherry", "main", merged); strings.Contains(out, "+") {
		t.Fatalf("setup: every ordinary commit should be upstream, got %q", out)
	}

	git(t, dir, "switch", "-q", "main")
	base := git(t, dir, "rev-parse", "HEAD")
	t.Chdir(dir)

	for name, tc := range map[string]struct {
		tip  string
		want bool
	}{
		"cherry-picked":     {picked, true},
		"ancestor":          {git(t, dir, "rev-parse", "HEAD~1"), true},
		"unique commit":     {unique, false},
		"unreachable merge": {merged, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := branchInBase(t.Context(), base, tc.tip)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("branchInBase = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllInBase(t *testing.T) {
	if !allInBase("") || !allInBase("- aaa\n- bbb\n") {
		t.Error("only '-' lines means every commit is upstream")
	}
	if allInBase("- aaa\n+ bbb\n") {
		t.Error("a '+' line means a commit is missing upstream")
	}
}

func TestParseRemoteURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/hashicorp/terraform-provider-aws.git": "hashicorp/terraform-provider-aws",
		"https://github.com/hashicorp/terraform-provider-awscc":   "hashicorp/terraform-provider-awscc",
		"git@github.com:YakDriver/hedgetrimmer.git":               "YakDriver/hedgetrimmer",
		"ssh://git@github.com/YakDriver/my.repo.git":              "YakDriver/my.repo",
	} {
		got, err := parseRemoteURL(url)
		if err != nil || got != want {
			t.Errorf("parseRemoteURL(%q) = %q, %v; want %q", url, got, err, want)
		}
	}
	if _, err := parseRemoteURL("https://gitlab.com/a/b.git"); err == nil {
		t.Error("want error for non-GitHub remote")
	}
}

func TestWriteMarkdown(t *testing.T) {
	cfg := testConfig(t, testHCL+`header = "Tracking: #1"`)
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	res := &Result{Repo: "acme/widgets", Base: "main", Branches: []Branch{
		{Name: "newer-bot", Date: day(5), Age: 5, Owner: "bot", Status: StatusBot, Bucket: "R1", SHA: "b2"},
		{Name: "old-bot", Date: day(1), Age: 9, Owner: "bot", Status: StatusBot, Bucket: "R1", SHA: "b1", PR: &PR{Number: 7, State: "MERGED"}},
		{Name: "merged", Date: day(2), Owner: "gdavison", Status: StatusMaintainer, Bucket: "R2", InBase: true, SHA: "m"},
		{Name: "wip", Date: day(3), Owner: "jar-b", Status: StatusMaintainer, Bucket: "R6", SHA: "w", PR: &PR{Number: 8, State: "OPEN"}},
		{Name: "release/1.x", Date: day(1), Owner: "?", Status: StatusUnknown, Bucket: "releases", Protects: true, SHA: "r"},
	}}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, cfg, res); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"Tracking: #1\n\n",
		"**5** branches in `acme/widgets` (excluding `main`)",
		"| R2 | Content already in `main` (patch-equivalent), no open PR | 1 |",
		"| R4 | Current maintainer, stale >365d, no open PR | 0 |",
		"| releases | Release branches | 1 | n/a | n/a |",
		"## R1: Automation/bot, no open PR (2)",
		"- [ ] `old-bot`: 2026-01-01 (9d), #7 (merged), owner `bot` (bot), tip `b1`",
		"- [ ] `merged`: 2026-01-02 (0d), no PR, owner `gdavison` (maintainer), tip `m` **[content in `main`]**",
		"<details><summary>R6: Active: open PR (1)</summary>",
		"- `wip`: 2026-01-03 (0d), #8, owner `jar-b` (maintainer), tip `w`",
		"## releases: Release branches (1)\n\n- `release/1.x`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "`old-bot`") > strings.Index(out, "`newer-bot`") {
		t.Error("oldest branch should come first within a bucket")
	}
	if strings.Contains(out, "## R3") || strings.Contains(out, "## pages") {
		t.Error("empty buckets should have no section")
	}
}
