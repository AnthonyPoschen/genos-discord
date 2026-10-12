#!/usr/bin/env bash
# Pull the latest AnthonyPoschen/agent-skills into the user-level skills folder
# so Cursor cloud agents can use them. Never fails environment setup.
set -u
dest="$HOME/.cursor/skills"
tmp="$(mktemp -d)"
if git clone --depth 1 -q https://github.com/AnthonyPoschen/agent-skills "$tmp/agent-skills"; then
  mkdir -p "$dest"
  cp -R "$tmp/agent-skills/skills/." "$dest/"
  echo "agent-skills synced at $(git -C "$tmp/agent-skills" rev-parse --short HEAD) into $dest"
else
  echo "agent-skills sync failed; continuing without it" >&2
fi
rm -rf "$tmp"
exit 0
