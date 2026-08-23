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

# jq is required (the status line itself depends on it too). RUN it rather than just
# looking the name up: a jq that is present but cannot execute — broken install, wrong
# architecture, missing +x — is a real machine, and `command -v` calls that one present.
# Without this probe the validity check below fails for jq's reason and blames the
# user's file, telling them to go fix a settings.json that was never broken.
# statusline.sh makes the same call the same way.
printf '{}' | jq -e . >/dev/null 2>&1 || exit 0

settings="$HOME/.claude/settings.json"
mkdir -p "$HOME/.claude" 2>/dev/null || exit 0

# Follow a symlink to its target before writing. Dotfile setups symlink this file into a
# managed repo, and `mv` onto the link replaces the LINK with a regular file: the repo
# keeps the stale copy, stops receiving every later change, and nothing appears on screen
# — the status line still comes up, so it reads as success. One hop only, which is the
# shape dotfile managers create; `readlink -f` would chase further but is not portable to
# older macOS.
if [ -L "$settings" ]; then
  link=$(readlink "$settings" 2>/dev/null)
  case "$link" in
    /*) settings="$link" ;;
    ?*) settings="$HOME/.claude/$link" ;;
  esac
fi

current=""
if [ -s "$settings" ]; then
  # Exists and non-empty: must be valid JSON, or we refuse to touch it. jq is known to
  # run by now, so a failure here really is the file.
  if ! jq -e . "$settings" >/dev/null 2>&1; then
    echo "grafik-bar: ~/.claude/settings.json is not valid JSON; leaving it untouched." >&2
    exit 0
  fi
  current=$(jq -r '.statusLine.command // ""' "$settings")
fi

# Already pointing at the current plugin script → nothing to do (fast, silent).
if [ "$current" = "$desired" ]; then
  exit 0
fi

# Set only .statusLine, preserving all other settings. Write atomically — and write
# NOTHING until the transform has succeeded. A missing settings.json starts from `{}` on
# stdin instead of being created up front, so a run that cannot finish leaves the file
# system exactly as it found it (the old order created the file, then reported "left
# unchanged").
filter='.statusLine = {type: "command", command: $cmd}'
tmp=$(mktemp "${settings}.XXXXXX" 2>/dev/null) || exit 0
built=0
if [ -s "$settings" ]; then
  jq --arg cmd "$desired" "$filter" "$settings" > "$tmp" 2>/dev/null && built=1
else
  printf '{}' | jq --arg cmd "$desired" "$filter" > "$tmp" 2>/dev/null && built=1
fi
if [ "$built" = 1 ]; then
  mv "$tmp" "$settings"
  echo "grafik-bar: status line configured → $script_path"
else
  rm -f "$tmp"
  echo "grafik-bar: could not update settings.json; left unchanged." >&2
fi
exit 0
