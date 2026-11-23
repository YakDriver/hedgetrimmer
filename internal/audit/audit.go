// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

// Package audit collects a repository's branches, sorts them into cleanup
// buckets, and renders a markdown worksheet. It never modifies the repository.
package audit

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/YakDriver/hedgetrimmer/internal/config"
)

// Owner statuses.
const (
	StatusMaintainer = "maintainer"
	StatusOther      = "other"
	StatusBot        = "bot"
	StatusUnknown    = "unknown"
)

// statusOrder ranks owner candidates, best first. A maintainer who touched a
// branch is its steward. Otherwise automation is the more useful signal than
// a non-maintainer human: bots like Copilot open PRs whose commits are
// attributed to an ordinary user account.
var statusOrder = []string{StatusMaintainer, StatusBot, StatusOther, StatusUnknown}

// Branch is one remote branch and everything the report says about it.
type Branch struct {
	Name     string
	SHA      string
	Date     time.Time
	Age      int // whole days since Date
	InBase   bool
	PR       *PR
	Owner    string
	Status   string
	Bucket   string
	Protects bool // Bucket is a protect block rather than R1–R6
}

// PR is the pull request a branch is attributed to.
type PR struct {
	Number int
	State  string // OPEN, CLOSED, or MERGED
	Author string // "app/<slug>" for GitHub Apps
	Bot    bool
}

// Result is an audit of one repository.
type Result struct {
	Repo     string
	Base     string
	Branches []Branch
}

// Run audits the repository described by cfg.
func Run(ctx context.Context, cfg *config.Config, roster map[string]bool, now time.Time) (*Result, error) {
	repo := cfg.Repo
	if repo == "" {
		var err error
		if repo, err = repoFromRemote(ctx, cfg.Remote); err != nil {
			return nil, err
		}
	}

	base, baseSHA, refs, err := fetchRefs(ctx, repo)
	if err != nil {
		return nil, err
	}

	branches := make([]Branch, 0, len(refs))
	for _, r := range refs {
		if r.Name == base {
			continue
		}
		branches = append(branches, r.branch(cfg, roster, now))
	}

	if err := markInBase(ctx, cfg.Remote, baseSHA, branches); err != nil {
		return nil, err
	}
	for i := range branches {
		classify(cfg, &branches[i])
	}
	return &Result{Repo: repo, Base: base, Branches: branches}, nil
}

// classify sets Bucket. Protect blocks win, in config order; then the first
// matching rank, R6 through R1 in order of how sure we are to keep it.
func classify(cfg *config.Config, b *Branch) {
	for _, p := range cfg.Protect {
		if p.Matches(b.Name) {
			b.Bucket, b.Protects = p.Name, true
			return
		}
	}
	switch {
	case b.PR != nil && b.PR.State == "OPEN":
		b.Bucket = config.RankOpenPR
	case b.Status == StatusBot:
		b.Bucket = config.RankBot
	case b.InBase:
		b.Bucket = config.RankInDefault
	case b.Status == StatusOther || b.Status == StatusUnknown:
		b.Bucket = config.RankNoOwner
	case b.Age > cfg.StaleDays:
		b.Bucket = config.RankStale
	default:
		b.Bucket = config.RankRecent
	}
}

// owner picks the most current steward among the PR author and the tip
// commit's author.
func owner(cfg *config.Config, roster map[string]bool, pr *PR, c commit) (status, who string) {
	type cand struct{ status, who string }
	var cands []cand

	if pr != nil && pr.Author != "" {
		switch {
		case pr.Bot || cfg.IsBotLogin(pr.Author):
			cands = append(cands, cand{StatusBot, pr.Author})
		default:
			cands = append(cands, cand{loginStatus(roster, pr.Author), pr.Author})
		}
	}

	login := c.Author.User.Login
	if login == "" {
		login = cfg.Login(c.Author.Email)
	}
	switch {
	case cfg.IsBotLogin(login) || cfg.IsBotEmail(c.Author.Email):
		cands = append(cands, cand{StatusBot, "bot"})
	case login != "":
		cands = append(cands, cand{loginStatus(roster, login), login})
	}

	if len(cands) == 0 {
		return StatusUnknown, "?"
	}
	best := slices.MinFunc(cands, func(a, b cand) int {
		return slices.Index(statusOrder, a.status) - slices.Index(statusOrder, b.status)
	})
	return best.status, best.who
}

func loginStatus(roster map[string]bool, login string) string {
	if roster[strings.ToLower(login)] {
		return StatusMaintainer
	}
	return StatusOther
}

// GitHub GraphQL response shapes, trimmed to what the audit reads.
type (
	ref struct {
		Name   string `json:"name"`
		Target commit `json:"target"`
		// OpenPRs and RecentPRs are aliases of associatedPullRequests. Open
		// PRs are queried separately so many newer closed PRs can't hide one.
		OpenPRs struct {
			Nodes []prNode `json:"nodes"`
		} `json:"openPRs"`
		RecentPRs struct {
			Nodes []prNode `json:"nodes"`
		} `json:"recentPRs"`
	}
	prNode struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Author *struct {
			Login    string `json:"login"`
			Typename string `json:"__typename"`
		} `json:"author"`
	}
	commit struct {
		OID           string    `json:"oid"`
		CommittedDate time.Time `json:"committedDate"`
		Author        struct {
			Email string `json:"email"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"author"`
	}
	refsPage struct {
		Data struct {
			Repository *struct {
				DefaultBranchRef *struct {
					Name   string `json:"name"`
					Target struct {
						OID string `json:"oid"`
					} `json:"target"`
				} `json:"defaultBranchRef"`
				Refs struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []ref `json:"nodes"`
				} `json:"refs"`
			} `json:"repository"`
		} `json:"data"`
	}
)

func (r ref) branch(cfg *config.Config, roster map[string]bool, now time.Time) Branch {
	b := Branch{
		Name: r.Name,
		SHA:  r.Target.OID,
		Date: r.Target.CommittedDate,
		Age:  int(now.Sub(r.Target.CommittedDate).Hours() / 24),
		PR:   r.pr(),
	}
	b.Status, b.Owner = owner(cfg, roster, b.PR, r.Target)
	return b
}

// pr returns the open PR if there is one, otherwise the highest-numbered of
// the most recently created. PR numbers are assigned at creation, but
// GitHub's CREATED_AT order doesn't break ties by number.
func (r ref) pr() *PR {
	nodes := r.OpenPRs.Nodes
	if len(nodes) == 0 {
		nodes = r.RecentPRs.Nodes
	}
	if len(nodes) == 0 {
		return nil
	}
	n := slices.MaxFunc(nodes, func(a, b prNode) int { return cmp.Compare(a.Number, b.Number) })
	p := &PR{Number: n.Number, State: n.State}
	if n.Author != nil {
		p.Author = n.Author.Login
		if n.Author.Typename == "Bot" {
			p.Author, p.Bot = "app/"+n.Author.Login, true
		}
	}
	return p
}

const refsQuery = `query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name target { oid } }
    refs(refPrefix: "refs/heads/", first: 100, after: $cursor) {
      pageInfo { hasNextPage endCursor }
      nodes {
        name
        target {
          ... on Commit {
            oid
            committedDate
            author { email user { login } }
          }
        }
        openPRs: associatedPullRequests(first: 10, states: [OPEN]) {
          nodes { number state author { login __typename } }
        }
        recentPRs: associatedPullRequests(first: 10, orderBy: {field: CREATED_AT, direction: DESC}) {
          nodes { number state author { login __typename } }
        }
      }
    }
  }
}`

// fetchRefs lists every branch with one paginated GraphQL query instead of
// one "gh pr list" call per branch. It also returns the default branch's name
// and tip, which is the baseline for markInBase.
func fetchRefs(ctx context.Context, repo string) (base, baseSHA string, refs []ref, err error) {
	owner, name, _ := strings.Cut(repo, "/")
	cursor := ""
	for {
		out, err := run(ctx, "gh", refsArgs(owner, name, cursor)...)
		if err != nil {
			return "", "", nil, err
		}
		page, err := parseRefsPage(out)
		if err != nil {
			return "", "", nil, fmt.Errorf("repository %s: %w", repo, err)
		}
		r := page.Data.Repository
		if r.DefaultBranchRef != nil {
			base, baseSHA = r.DefaultBranchRef.Name, r.DefaultBranchRef.Target.OID
		}
		refs = append(refs, r.Refs.Nodes...)
		if !r.Refs.PageInfo.HasNextPage {
			break
		}
		cursor = r.Refs.PageInfo.EndCursor
	}
	if base == "" || baseSHA == "" {
		return "", "", nil, fmt.Errorf("repository %s has no default branch", repo)
	}
	return base, baseSHA, refs, nil
}

// refsArgs uses -f, not -F, for every variable: -F converts values like
// "true", "null", or "2026" to non-strings, which valid repo names can be.
func refsArgs(owner, name, cursor string) []string {
	args := []string{"api", "graphql", "-f", "query=" + refsQuery, "-f", "owner=" + owner, "-f", "name=" + name}
	if cursor != "" {
		args = append(args, "-f", "cursor="+cursor)
	}
	return args
}

func parseRefsPage(data []byte) (*refsPage, error) {
	var page refsPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("decoding GitHub response: %w", err)
	}
	if page.Data.Repository == nil {
		return nil, errors.New("not found or not accessible")
	}
	return &page, nil
}

// markInBase sets InBase for branches whose every commit is patch-equivalent
// to one reachable from baseSHA (git cherry), which also catches rebase-merged
// and cherry-picked work. baseSHA is the audited repository's default branch
// tip from GitHub, not a local remote-tracking ref, which could belong to a
// fork or be out of date.
//
// git cherry skips merge commits, and a merge can carry its own changes in
// conflict resolutions, so a branch with merges not reachable from baseSHA is
// never marked InBase.
//
// It needs the commits locally, so it checks for them first and asks for a
// fetch rather than misreporting.
func markInBase(ctx context.Context, remote, baseSHA string, branches []Branch) error {
	shas := []string{baseSHA}
	for _, b := range branches {
		shas = append(shas, b.SHA)
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch-check")
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	if missing := bytes.Count(out, []byte(" missing\n")); missing > 0 {
		return fmt.Errorf("%d commits (default branch or branch tips) aren't in the local clone; fetch the audited repository, e.g. \"git fetch %s\"", missing, remote)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, runtime.GOMAXPROCS(0))
	)
	for i := range branches {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			inBase, err := branchInBase(ctx, baseSHA, branches[i].SHA)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			branches[i].InBase = inBase
		})
	}
	wg.Wait()
	return firstErr
}

func branchInBase(ctx context.Context, baseSHA, tip string) (bool, error) {
	merges, err := run(ctx, "git", "rev-list", "--count", "--merges", baseSHA+".."+tip)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(merges)) != "0" {
		return false, nil
	}
	cherry, err := run(ctx, "git", "cherry", baseSHA, tip)
	if err != nil {
		return false, err
	}
	return allInBase(string(cherry)), nil
}

// allInBase reports whether git cherry output has no "+" lines, which mark
// commits without an equivalent upstream.
func allInBase(cherry string) bool {
	for line := range strings.Lines(cherry) {
		if strings.HasPrefix(line, "+") {
			return false
		}
	}
	return true
}

var remoteURLRE = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

func repoFromRemote(ctx context.Context, remote string) (string, error) {
	out, err := run(ctx, "git", "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return parseRemoteURL(strings.TrimSpace(string(out)))
}

func parseRemoteURL(url string) (string, error) {
	m := remoteURLRE.FindStringSubmatch(url)
	if m == nil {
		return "", fmt.Errorf("can't infer GitHub repo from remote URL %q; set repo in the config", url)
	}
	return m[1] + "/" + m[2], nil
}

// run executes a command and returns stdout. Failures include stderr, since a
// silently failed git or gh call would misclassify branches.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
