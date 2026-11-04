#!/usr/bin/env bash
# Fails when a tracked text file contains the em dash character (U+2014).
# Project convention: use a hyphen or rephrase.
set -euo pipefail

emdash="$(printf '\342\200\224')"
if matches="$(git grep -n -I -F -e "$emdash" -- . ':!LICENSE')"; then
  echo "Em dash characters found (use '-' or rephrase):" >&2
  echo "$matches" >&2
  exit 1
fi
