#!/usr/bin/env bash
# Check that a commit message is a conventional commit of a type cliff.toml
# allows, by asking git-cliff: the rules that write the changelog are the
# rules a commit is held to, so the two cannot drift apart.
#
# Usage: scripts/check-commit-msg.sh FILE     (the commit-msg hook)
#        scripts/check-commit-msg.sh -m MESSAGE
set -euo pipefail

if ! command -v git-cliff >/dev/null; then
    echo "error: git-cliff is not installed; see https://git-cliff.org" >&2
    exit 1
fi

if [ "${1:-}" = -m ]; then
    msg=${2:?usage: check-commit-msg.sh -m MESSAGE}
else
    # What git commits from the file: no comment lines, and nothing below
    # the scissors line `git commit --verbose` adds.
    msg=$(sed -e '/^# -* >8 -*$/,$d' -e '/^#/d' "${1:?usage: check-commit-msg.sh FILE}")
fi

# git-cliff reads commits, not messages, so the message becomes one: a
# commit object no ref points at, which git collects as garbage later.
tree=$(git hash-object -t tree /dev/null)
if parent=$(git rev-parse --quiet --verify HEAD); then
    commit=$(git commit-tree "$tree" -p "$parent" -m "$msg")
    range=$parent..$commit
else
    commit=$(git commit-tree "$tree" -m "$msg")
    range=$commit
fi

root=$(git rev-parse --show-toplevel)
if ! git cliff --config "$root/cliff.toml" "$range" -o /dev/null 2>/dev/null; then
    cat >&2 <<EOF
error: not a conventional commit of an allowed type:

    $(printf '%s' "$msg" | head -1)

Write it as type(scope): summary, where type is one of feat, fix, perf,
revert, build, chore, ci, docs, refactor, style, or test, and mark a breaking
change with type!: or a BREAKING CHANGE: footer. See
https://www.conventionalcommits.org and the commit_parsers in cliff.toml.
EOF
    exit 1
fi
