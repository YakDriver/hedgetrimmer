<!-- Copyright IBM Corp. 2026 -->
<!-- SPDX-License-Identifier: MPL-2.0 -->

# hedgetrimmer

hedgetrimmer audits the branches of a GitHub repository and prints a markdown worksheet for cleaning them up. Branches that are quick to check and likely deletable come first. Paste the worksheet into an issue and tick items off as you delete them.

hedgetrimmer is read-only. It never deletes, pushes, or changes anything.

## Install

```console
go install github.com/YakDriver/hedgetrimmer@latest
```

It needs `git` and an authenticated [`gh`](https://cli.github.com/) CLI.

## Use

Run it inside a clone, after fetching:

```console
git fetch origin --prune
hedgetrimmer > audit.md
```

With no config, hedgetrimmer infers the repository from the `origin` URL and treats every non-bot owner as a non-maintainer. Write a starter config with:

```console
hedgetrimmer init        # writes .hedgetrimmer.hcl, every option commented
```

| Flag | Meaning |
|---|---|
| `--config` | Config file. Default: `.hedgetrimmer.hcl` in the working directory, if present. |
| `--repo` | `owner/name`, overriding the config and the remote URL. |
| `-o`, `--output` | Write the report to a file instead of stdout. |

## Buckets

Each branch except the default branch lands in exactly one bucket. Protect blocks are checked first, in config order. Then the first matching rank applies, in this order: R6, R1, R2, R3, then R4 or R5.

| Rank | Bucket | Effort to validate | Likelihood deletable |
|---|---|---|---|
| R1 | Automation/bot, no open PR | trivial | very high |
| R2 | Content already in the default branch, no open PR | trivial | very high |
| R3 | No current owner (former or external), no open PR | low–medium | high |
| R4 | Current maintainer, stale (> `stale_days`), no open PR | medium | medium |
| R5 | Current maintainer, recent, no open PR | medium | low |
| R6 | Open PR | n/a | keep |
| _protect name_ | Matches a `protect` block | n/a | n/a |

How the signals are computed:

- **PR**: the branch's open PR if it has one, otherwise its highest-numbered PR.
- **Owner**: the best of two candidates, the PR author and the tip commit's author. A maintainer ranks first, then a bot, then anyone else. The commit author is the GitHub account the commit is linked to, or the `identities` entry for its email.
- **Bot**: a GitHub App PR author, or a login or email containing `[bot]` or a configured `bot_logins`/`bot_emails` marker.
- **Content in default branch**: `git cherry` finds a patch-equivalent commit in the default branch for every commit on the branch. This catches squash and rebase merges that ancestry checks miss. A branch that is merely an ancestor also qualifies.

Branch, PR, and author data comes from a single paginated GitHub GraphQL query. `git cherry` runs locally, so hedgetrimmer refuses to run if any branch tip is missing from the clone.

## Configuration

`.hedgetrimmer.hcl` is HCL. Every attribute is optional, and relative paths resolve against the config file's directory.

```hcl
repo       = "hashicorp/terraform-provider-aws" # default: from the remote URL
remote     = "origin"                           # default
stale_days = 365                                # default

# Maintainer roster. These three sources are merged.
maintainers      = ["octocat"]
maintainers_file = "MAINTAINERS" # one login per line, "#" comments

# github.com/<login> links in one markdown section. The section runs to
# the next heading at the same or a higher level.
maintainers_markdown {
  file    = "docs/faq.md"
  section = "Who are the maintainers?"
}

# Commit emails GitHub can't link to an account.
identities = {
  "dirk.avery@gmail.com" = "YakDriver"
}

bot_emails = ["changelogbot@hashicorp.com"]
bot_logins = ["renovate"]

# Go regular expressions matched against branch names. Each block gets its
# own report section.
protect "X1" {
  title    = "Cannot delete: `release*` / `v*` (org rule)"
  patterns = ["^release", "^v[0-9]"]
}

protect "X2" {
  title    = "Protected infra: `gh-pages`"
  patterns = ["^gh-pages$"]
}

header   = "Living worksheet for #48800."
collapse = ["R5", "R6"] # default; rendered inside <details>
```

Unknown attributes, invalid regular expressions, malformed `repo` values, and a `maintainers_markdown` section without any logins are errors. A typo should fail the run, not quietly misclassify branches.

## Development

```console
make ci   # tidy, build, test, vet, modern-check, copyright
```
