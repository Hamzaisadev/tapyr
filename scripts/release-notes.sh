#!/usr/bin/env bash
# Prints the section of a version in CHANGELOG.md (Keep a Changelog), without
# its heading, for the release notes. A prerelease (0.1.0-rc.1) without a
# section of its own takes the [Unreleased] section: release candidates
# test the release of what is about to become the version. Fails when the
# section is missing or empty, so that a tag is never released without its
# changelog. Relative links (to files of the repository) become links to
# the files at the tag: on the release page, they would point nowhere.
# Usage: scripts/release-notes.sh 0.1.0 [CHANGELOG.md]
set -euo pipefail

version="${1:?usage: $0 VERSION [CHANGELOG.md]}"
version="${version#v}"
file="${2:-CHANGELOG.md}"

section() {
  awk -v v="$1" '
    /^## \[/ { if (found) exit; found = (index($0, "## [" v "]") == 1); next }
    /^\[[^]]+\]: / { if (found) exit }
    found { print }
  ' "$file" | sed -e '/./,$!d'
}

notes="$(section "$version")"
case "$version" in
  *-*) [ -n "$(printf '%s' "$notes" | tr -d '[:space:]')" ] || notes="$(section Unreleased)" ;;
esac

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "no section for version $version in $file" >&2
  exit 1
fi
# ](docs/x.md#y) -> ](https://github.com/Kentrow/tapyr/blob/vX.Y.Z/docs/x.md#y);
# absolute URLs, anchors and mailto links are kept.
repo="https://github.com/Kentrow/tapyr/blob/v${version}"
printf '%s\n' "$notes" | sed -E "s#\]\(([^)#:/][^):]*)\)#](${repo}/\1)#g"
