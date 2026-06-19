#!/usr/bin/env bash
# grafik-bar — keep the user's status line pointed at this plugin's statusline.sh.
#
# Runs on SessionStart (see hooks/hooks.json). Idempotent: it only writes
# ~/.claude/settings.json when the statusLine command is missing or stale — e.g.
# after a plugin update moves the plugin path. It touches ONLY the statusLine key,
# preserves everything else, and bails out quietly on any error so it can never
# disrupt a session.

# Resolve this plugin's statusline.sh. Prefer CLAUDE_PLUGIN_ROOT (set in the hook
# environment); fall back to this script's own directory for manual runs.
root="${CLAUDE_PLUGIN_ROOT:-}"
if [ -z "$root" ]; then
  root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
script_path="$root/scripts/statusline.sh"
desired="bash \"$script_path\""

# jq is required (the status line itself depends on it too). No jq → do nothing.
command -v jq >/dev/null 2>&1 || exit 0

settings="$HOME/.claude/settings.json"
mkdir -p "$HOME/.claude"

current=""
if [ -s "$settings" ]; then
  # Exists and non-empty: must be valid JSON, or we refuse to touch it.
  if ! jq -e . "$settings" >/dev/null 2>&1; then
    echo "grafik-bar: ~/.claude/settings.json is not valid JSON; leaving it untouched." >&2
    exit 0
  fi
  current=$(jq -r '.statusLine.command // ""' "$settings")
else
  printf '{}\n' > "$settings"
fi

# Already pointing at the current plugin script → nothing to do (fast, silent).
if [ "$current" = "$desired" ]; then
  exit 0
fi

# Set only .statusLine, preserving all other settings. Write atomically.
tmp=$(mktemp "${settings}.XXXXXX") || exit 0
if jq --arg cmd "$desired" '.statusLine = {type: "command", command: $cmd}' "$settings" > "$tmp" 2>/dev/null; then
  mv "$tmp" "$settings"
  echo "grafik-bar: status line configured → $script_path"
else
  rm -f "$tmp"
  echo "grafik-bar: could not update settings.json; left unchanged." >&2
fi
exit 0
