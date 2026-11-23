// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

// Package config loads and validates hedgetrimmer's HCL configuration.
package config

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsimple"
)

// DefaultFile is the config file name looked up in the working directory.
const DefaultFile = ".hedgetrimmer.hcl"

// Template is the commented starter config written by "hedgetrimmer init".
//
//go:embed template.hcl
var Template []byte

// Rank IDs for the built-in buckets, ordered from most to least deletable.
const (
	RankBot        = "R1"
	RankInDefault  = "R2"
	RankNoOwner    = "R3"
	RankStale      = "R4"
	RankRecent     = "R5"
	RankOpenPR     = "R6"
	defaultRemote  = "origin"
	defaultStale   = 365
	defaultBotMark = "[bot]"
)

// Ranks lists the built-in bucket IDs in report order.
var Ranks = []string{RankBot, RankInDefault, RankNoOwner, RankStale, RankRecent, RankOpenPR}

// Config is the top-level hedgetrimmer configuration.
type Config struct {
	// Repo is "owner/name". Empty means infer it from the Remote URL.
	Repo string `hcl:"repo,optional"`

	// Remote is the git remote used to infer Repo. Default "origin".
	Remote string `hcl:"remote,optional"`

	// StaleDays splits maintainer-owned branches without an open PR into
	// stale (R4) and recent (R5). Default 365.
	StaleDays int `hcl:"stale_days,optional"`

	// Maintainers, MaintainersFile, and MaintainersMarkdown are merged into
	// one roster of current maintainer logins (case-insensitive).
	Maintainers         []string  `hcl:"maintainers,optional"`
	MaintainersFile     string    `hcl:"maintainers_file,optional"`
	MaintainersMarkdown *Markdown `hcl:"maintainers_markdown,block"`

	// Identities maps commit emails to GitHub logins for commits GitHub
	// can't link to an account.
	Identities map[string]string `hcl:"identities,optional"`

	// BotEmails and BotLogins are case-insensitive substrings that mark a
	// commit email or login as automation. They add to the built-in "[bot]"
	// marker; GitHub App PR authors are always bots.
	BotEmails []string `hcl:"bot_emails,optional"`
	BotLogins []string `hcl:"bot_logins,optional"`

	// Protect blocks list branches that can't or shouldn't be deleted. Each
	// becomes its own report section after the ranked buckets.
	Protect []Protect `hcl:"protect,block"`

	// Header is free-form markdown placed at the top of the report.
	Header string `hcl:"header,optional"`

	// Collapse lists bucket IDs (R1–R6 or protect block names) rendered
	// inside a <details> element. Nil means ["R5", "R6"].
	Collapse []string `hcl:"collapse,optional"`

	// dir is the config file's directory; relative paths resolve against it.
	dir string
}

// Markdown reads maintainer logins from github.com/<login> links in one
// section of a markdown file.
type Markdown struct {
	File    string `hcl:"file"`
	Section string `hcl:"section"`
}

// Protect describes a group of branches excluded from deletion.
type Protect struct {
	Name     string   `hcl:"name,label"`
	Title    string   `hcl:"title,optional"`
	Patterns []string `hcl:"patterns"`

	res []*regexp.Regexp
}

// Matches reports whether branch matches any of the block's patterns.
func (p *Protect) Matches(branch string) bool {
	return slices.ContainsFunc(p.res, func(re *regexp.Regexp) bool { return re.MatchString(branch) })
}

// Load reads path. When path is empty, it reads DefaultFile if present and
// otherwise returns defaults. The result is validated.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultFile
	}

	var c Config
	src, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := hclsimple.Decode(filepath.Base(path), src, nil, &c); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
	default:
		return nil, fmt.Errorf("reading config: %w", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	c.dir = filepath.Dir(abs)

	if err := c.finish(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) finish() error {
	if c.Remote == "" {
		c.Remote = defaultRemote
	}
	if c.StaleDays == 0 {
		c.StaleDays = defaultStale
	}
	if c.StaleDays < 0 {
		return fmt.Errorf("stale_days must be positive, got %d", c.StaleDays)
	}
	if c.Repo != "" {
		if err := CheckRepo(c.Repo); err != nil {
			return err
		}
	}
	if c.Collapse == nil {
		c.Collapse = []string{RankRecent, RankOpenPR}
	}

	ids := slices.Clone(Ranks)
	for i := range c.Protect {
		p := &c.Protect[i]
		if slices.Contains(ids, p.Name) {
			return fmt.Errorf("protect %q: name duplicates another bucket", p.Name)
		}
		ids = append(ids, p.Name)
		if len(p.Patterns) == 0 {
			return fmt.Errorf("protect %q: patterns is empty", p.Name)
		}
		for _, pat := range p.Patterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				return fmt.Errorf("protect %q: %w", p.Name, err)
			}
			p.res = append(p.res, re)
		}
		if p.Title == "" {
			p.Title = "Protected: " + p.Name
		}
	}
	for _, id := range c.Collapse {
		if !slices.Contains(ids, id) {
			return fmt.Errorf("collapse: unknown bucket %q (want one of %s)", id, strings.Join(ids, ", "))
		}
	}

	c.BotEmails = append([]string{defaultBotMark}, c.BotEmails...)
	c.BotLogins = append([]string{defaultBotMark}, c.BotLogins...)
	return nil
}

// CheckRepo reports whether repo has the form "owner/name".
func CheckRepo(repo string) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("repo must be \"owner/name\", got %q", repo)
	}
	return nil
}

// Roster returns the merged set of maintainer logins, lowercased.
func (c *Config) Roster() (map[string]bool, error) {
	m := map[string]bool{}
	for _, l := range c.Maintainers {
		m[strings.ToLower(l)] = true
	}
	if c.MaintainersFile != "" {
		logins, err := readList(c.path(c.MaintainersFile))
		if err != nil {
			return nil, fmt.Errorf("maintainers_file: %w", err)
		}
		for _, l := range logins {
			m[strings.ToLower(l)] = true
		}
	}
	if md := c.MaintainersMarkdown; md != nil {
		logins, err := readMarkdownSection(c.path(md.File), md.Section)
		if err != nil {
			return nil, fmt.Errorf("maintainers_markdown: %w", err)
		}
		if len(logins) == 0 {
			return nil, fmt.Errorf("maintainers_markdown: no github.com/<login> links in section %q of %s", md.Section, md.File)
		}
		for _, l := range logins {
			m[strings.ToLower(l)] = true
		}
	}
	return m, nil
}

func (c *Config) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// readList reads one entry per line; blank lines and "#" comments are skipped.
func readList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	loginRE   = regexp.MustCompile(`github\.com/([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)\b`)
)

// readMarkdownSection returns logins linked in the first section whose
// heading contains section (case-insensitive). The section runs to the next
// heading at the same or a higher level.
func readMarkdownSection(path, section string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	want := strings.ToLower(section)
	level := 0 // heading level of the matched section; 0 means not inside it
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if h := headingRE.FindStringSubmatch(line); h != nil {
			if level > 0 && len(h[1]) <= level {
				break
			}
			if level == 0 && strings.Contains(strings.ToLower(h[2]), want) {
				level = len(h[1])
			}
			continue
		}
		if level == 0 {
			continue
		}
		for _, m := range loginRE.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out, sc.Err()
}

// IsBotEmail reports whether email matches a bot marker.
func (c *Config) IsBotEmail(email string) bool { return containsAny(email, c.BotEmails) }

// IsBotLogin reports whether login matches a bot marker.
func (c *Config) IsBotLogin(login string) bool { return containsAny(login, c.BotLogins) }

func containsAny(s string, subs []string) bool {
	s = strings.ToLower(s)
	return s != "" && slices.ContainsFunc(subs, func(sub string) bool {
		return strings.Contains(s, strings.ToLower(sub))
	})
}

// Login maps a commit email to a login using Identities.
func (c *Config) Login(email string) string {
	for e, l := range c.Identities {
		if strings.EqualFold(e, email) {
			return l
		}
	}
	return ""
}
