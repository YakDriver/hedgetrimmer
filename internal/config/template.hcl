# hedgetrimmer configuration. Every setting is optional.
# Docs: https://github.com/YakDriver/hedgetrimmer

# GitHub repository as "owner/name". Default: inferred from the remote URL.
# repo = "owner/name"

# Git remote whose branches are audited. Default: "origin".
# remote = "origin"

# Maintainer-owned branches with no open PR are "stale" (R4) after this many
# days since their last commit, otherwise "recent" (R5). Default: 365.
# stale_days = 365

# Current maintainers. Branches owned by anyone else, with no open PR, land in
# R3 ("no current owner"). The three sources below are merged.
# maintainers      = ["octocat"]
# maintainers_file = "MAINTAINERS"   # one login per line, "#" comments

# Read logins from github.com/<login> links in one section of a markdown file.
# maintainers_markdown {
#   file    = "docs/faq.md"
#   section = "Who are the maintainers?"
# }

# Map commit emails GitHub can't link to an account onto logins.
# identities = {
#   "me@example.com" = "octocat"
# }

# Extra case-insensitive substrings marking commit emails or logins as bots.
# "[bot]" is always included, and GitHub App PR authors are always bots.
# bot_emails = ["changelogbot@example.com"]
# bot_logins = ["renovate"]

# Branches that can't or shouldn't be deleted. Each block becomes its own
# report section. Patterns are Go regular expressions matched against the
# branch name. The default branch is always excluded.
# protect "releases" {
#   title    = "Release branches (org rule prevents deletion)"
#   patterns = ["^release", "^v[0-9]"]
# }
#
# protect "pages" {
#   title    = "GitHub Pages"
#   patterns = ["^gh-pages$"]
# }

# Markdown placed at the top of the report.
# header = "Living worksheet for #123."

# Buckets rendered collapsed inside <details>. Default: ["R5", "R6"].
# collapse = ["R5", "R6"]
