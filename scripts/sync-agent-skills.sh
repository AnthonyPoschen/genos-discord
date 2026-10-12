#!/usr/bin/env bash
# Sync project skills from AnthonyPoschen/agent-skills into .cursor/skills/.
# Idempotent: exits 0 with no changes when already current.
set -euo pipefail
REPO_URL="${AGENT_SKILLS_REPO:-https://github.com/AnthonyPoschen/agent-skills.git}"
REF="${AGENT_SKILLS_REF:-master}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/.cursor/skills"
VERSION_FILE="$DEST/.agent-skills-version"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
git clone --quiet --depth 1 --branch "$REF" "$REPO_URL" "$tmp/src"
sha="$(git -C "$tmp/src" rev-parse HEAD)"
[ -d "$tmp/src/skills" ] || { echo "no skills/ in source" >&2; exit 1; }
mkdir -p "$DEST"
rsync -a --delete --checksum --exclude '/.agent-skills-version' "$tmp/src/skills/" "$DEST/"
new="$sha
Synced from AnthonyPoschen/agent-skills; do not edit here, change upstream."
if [ ! -f "$VERSION_FILE" ] || [ "$(cat "$VERSION_FILE")" != "$new" ]; then
  printf '%s\n' "$new" > "$VERSION_FILE"
fi
echo "agent-skills synced at $sha"
