// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsimple"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_missingDefaultUsesDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Remote != "origin" || c.StaleDays != 365 || !slices.Equal(c.Collapse, []string{"R5", "R6"}) {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestLoad_missingExplicitFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.hcl")); err == nil {
		t.Fatal("want error for missing explicit config")
	}
}

func TestLoad_full(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "MAINTAINERS", "# team\nAlice\n\nbob # lead\n")
	write(t, dir, "faq.md", `# FAQ

## Who are the maintainers?

* Carol - [@carol](https://github.com/Carol)
* Dan - [@dan-x](https://github.com/dan-x)

### Emeritus

* Old - https://github.com/oldtimer

## Other

* [@zed](https://github.com/zed)
`)
	p := write(t, dir, ".hedgetrimmer.hcl", `
repo        = "acme/widgets"
stale_days  = 90
maintainers = ["eve"]
maintainers_file = "MAINTAINERS"
maintainers_markdown {
  file    = "faq.md"
  section = "who are the maintainers"
}
identities = { "Eve@Example.com" = "eve" }
bot_emails = ["changelogbot@"]
protect "releases" {
  patterns = ["^release", "^v[0-9]"]
}
collapse = ["R6", "releases"]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	roster, err := c.Roster()
	if err != nil {
		t.Fatal(err)
	}
	// "oldtimer" is under a deeper heading inside the section, so it counts;
	// "zed" is in the next section and doesn't.
	want := []string{"alice", "bob", "carol", "dan-x", "eve", "oldtimer"}
	got := make([]string, 0, len(roster))
	for l := range roster {
		got = append(got, l)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("roster = %v, want %v", got, want)
	}

	if c.Login("eve@example.com") != "eve" {
		t.Error("identity lookup should be case-insensitive")
	}
	if !c.IsBotEmail("changelogbot@acme.com") || !c.IsBotEmail("41898282+github-actions[bot]@users.noreply.github.com") {
		t.Error("bot email markers not applied")
	}
	if c.IsBotEmail("") || c.IsBotLogin("") {
		t.Error("empty string must not be a bot")
	}

	r := c.Protect[0]
	if r.Title != "Protected: releases" {
		t.Errorf("default title = %q", r.Title)
	}
	for name, want := range map[string]bool{"release/5.x": true, "v6-beta": true, "f-release": false, "vpc-fix": false} {
		if r.Matches(name) != want {
			t.Errorf("Matches(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestLoad_invalid(t *testing.T) {
	tests := map[string]struct{ body, wantErr string }{
		"bad repo":         {`repo = "widgets"`, "owner/name"},
		"negative stale":   {`stale_days = -1`, "stale_days"},
		"bad regex":        {`protect "x" { patterns = ["("] }`, `protect "x"`},
		"empty patterns":   {`protect "x" { patterns = [] }`, "patterns is empty"},
		"name collides":    {`protect "R1" { patterns = ["x"] }`, "duplicates"},
		"unknown collapse": {`collapse = ["R9"]`, "unknown bucket"},
		"unknown attr":     {`nope = 1`, "Unsupported argument"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			p := write(t, t.TempDir(), "c.hcl", tc.body)
			_, err := Load(p)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestRoster_emptyMarkdownSectionFails(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "faq.md", "## Maintainers\n\nnone listed\n")
	p := write(t, dir, "c.hcl", `maintainers_markdown {
  file    = "faq.md"
  section = "maintainers"
}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Roster(); err == nil {
		t.Fatal("want error when the section has no logins")
	}
}

// TestTemplate_decodes guards against "init" writing a config that fails to
// load once a user uncomments it.
func TestTemplate_decodes(t *testing.T) {
	// Example lines contain "=", "{", or "}"; prose comments don't.
	var hcl []string
	for line := range strings.Lines(string(Template)) {
		if s, ok := strings.CutPrefix(line, "# "); ok && strings.ContainsAny(s, "={}") {
			hcl = append(hcl, s)
		}
	}
	var c Config
	if err := hclsimple.Decode("template.hcl", []byte(strings.Join(hcl, "")), nil, &c); err != nil {
		t.Fatalf("uncommented template doesn't decode: %v\n%s", err, strings.Join(hcl, ""))
	}
	if err := c.finish(); err != nil {
		t.Fatal(err)
	}
	if len(c.Protect) != 2 || c.MaintainersMarkdown == nil {
		t.Errorf("template missing examples: %+v", c)
	}
}
