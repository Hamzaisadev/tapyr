#!/usr/bin/env bash
# Creates or updates the labels of .github/labels.yml in a GitHub repository.
# Usage: scripts/sync-labels.sh [owner/repo]   (default: the current repository)
set -euo pipefail

repo="${1:-}"
args=()
[ -n "$repo" ] && args=(--repo "$repo")
while IFS=$'\t' read -r name color description; do
  gh label create "$name" --color "$color" --description "$description" --force ${args[@]+"${args[@]}"}
done < <(sed -n 's/^- { name: \(.*\), color: \([0-9a-f]*\), description: \(.*\) }$/\1\t\2\t\3/p' .github/labels.yml)
