---
name: grafik-bar
description: "Claude Code status line setup - model, effort, context window, rate limits with responsive graphical bars"
user-invocable: true
argument-hint: "[optional: effort level - low/medium/high/xhigh/max, default: max]"
---

# Setup Status Line

Set up Claude Code status line with graphical bars.

## Display Items

- Login username (cyan)
- Workspace folder name (blue bold, `⌂`)
- Current git branch (green, `⎇`)
- Model name (magenta bold)
- Reasoning effort level — read live from the session (thunder icons, incl. xhigh/max)
- Context window usage (12-cell progress bar)
- 5-hour session limit (8-cell bar + reset countdown)
- 7-day weekly limit (8-cell bar + reset countdown)
- Session stats: cost (`$`), lines changed (`+added -removed`), elapsed time (`⏱`)

## Color Thresholds

- 0-39%: Green
- 40-69%: Cyan
- 70-89%: Yellow
- 90-100%: Red

## Responsive Layout

- Wide (>=120 cols): single line
- Medium (80-119): two lines (identity + model + effort + context, then limits)
- Narrow (<80): identity (user + folder + branch), then model + effort, then context, then limits

## Setup Instructions

Perform the following two steps:

### Step 1: Create statusline-command.sh

Create `~/.claude/statusline-command.sh` with the following content:

```bash
#!/usr/bin/env bash
# Claude Code status line - responsive graphical display

input=$(cat)

# --- Terminal width ---
cols=$(tput cols </dev/tty 2>/dev/null || stty size </dev/tty 2>/dev/null | awk '{print $2}' || echo 120)

# --- Extract fields ---
model=$(echo "$input" | jq -r '.model.display_name // "Unknown Model"')
# Effort: read the live session value from the status line payload (.effort.level),
# which now emits every level — low/medium/high/xhigh/max — and reflects mid-session
# /effort changes. Fall back to settings.json only for older Claude Code versions that
# don't emit effort; leave empty when the model has no effort param.
effort=$(echo "$input" | jq -r '.effort.level // empty')
[ -z "$effort" ] && effort=$(jq -r '.effortLevel // empty' ~/.claude/settings.json 2>/dev/null)
ctx_used=$(echo "$input" | jq -r '.context_window.used_percentage // empty')
five_pct=$(echo "$input" | jq -r '.rate_limits.five_hour.used_percentage // empty')
five_reset=$(echo "$input" | jq -r '.rate_limits.five_hour.resets_at // empty')
week_pct=$(echo "$input" | jq -r '.rate_limits.seven_day.used_percentage // empty')
week_reset=$(echo "$input" | jq -r '.rate_limits.seven_day.resets_at // empty')
user=$(claude auth status 2>/dev/null | jq -r '.email // empty' 2>/dev/null)

# Workspace folder (project root) + current git branch
ws_dir=$(echo "$input" | jq -r '.workspace.project_dir // .workspace.current_dir // .cwd // empty')
cur_dir=$(echo "$input" | jq -r '.workspace.current_dir // .cwd // .workspace.project_dir // empty')
folder=""; [ -n "$ws_dir" ] && folder=$(basename "$ws_dir")
branch=""; [ -n "$cur_dir" ] && branch=$(git --no-optional-locks -C "$cur_dir" branch --show-current 2>/dev/null)

# Session stats (cost / lines changed / duration)
cost=$(echo "$input" | jq -r '.cost.total_cost_usd // empty')
lines_added=$(echo "$input" | jq -r '.cost.total_lines_added // empty')
lines_removed=$(echo "$input" | jq -r '.cost.total_lines_removed // empty')
dur_ms=$(echo "$input" | jq -r '.cost.total_duration_ms // empty')

# --- ANSI colors ---
RST='\033[0m'
BOLD='\033[1m'
C_CYAN='\033[36m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_RED='\033[31m'
C_MAGENTA='\033[35m'
C_BLUE='\033[34m'
C_WHITE='\033[37m'
C_GRAY='\033[90m'

DOT="${C_GRAY}·${RST}"

# --- Helpers ---
render_bar() {
  local pct=${1:-0} width=$2 filled empty bar="" i
  filled=$(( pct * width / 100 ))
  empty=$(( width - filled ))
  for (( i=0; i<filled; i++ )); do bar+="█"; done
  if (( pct * width % 100 >= 50 )) && (( empty > 0 )); then
    bar+="▓"; empty=$(( empty - 1 ))
  fi
  for (( i=0; i<empty; i++ )); do bar+="░"; done
  printf '%s' "$bar"
}

pct_color() {
  local pct=$1
  if [ -z "$pct" ]; then printf '%s' "$C_GRAY"
  elif (( pct >= 90 )); then printf '%s' "$C_RED"
  elif (( pct >= 70 )); then printf '%s' "$C_YELLOW"
  elif (( pct >= 40 )); then printf '%s' "$C_CYAN"
  else printf '%s' "$C_GREEN"
  fi
}

format_reset() {
  local epoch=$1 now delta d h m
  [ -z "$epoch" ] && return
  now=$(date +%s)
  delta=$(( epoch - now ))
  (( delta <= 0 )) && printf 'now' && return
  d=$(( delta / 86400 )); h=$(( (delta % 86400) / 3600 )); m=$(( (delta % 3600) / 60 ))
  if (( d > 0 )); then printf '%dd' "$d"
  elif (( h > 0 )); then printf '%dh%02dm' "$h" "$m"
  else printf '%dm' "$m"
  fi
}

format_dur() {
  local ms=$1 s h m sec
  [ -z "$ms" ] && return
  s=$(( ms / 1000 ))
  h=$(( s / 3600 )); m=$(( (s % 3600) / 60 )); sec=$(( s % 60 ))
  if (( h > 0 )); then printf '%dh%02dm' "$h" "$m"
  elif (( m > 0 )); then printf '%dm%02ds' "$m" "$sec"
  else printf '%ds' "$sec"
  fi
}

effort_icon() {
  case "$1" in
    max)    printf '🔥⚡⚡⚡' ;;
    xhigh)  printf '⚡⚡⚡⚡' ;;
    high)   printf '⚡⚡⚡' ;;
    medium) printf '⚡⚡' ;;
    low)    printf '⚡' ;;
    *)      printf '⚡' ;;
  esac
}

# --- Build segments ---
if [ -n "$user" ]; then
  seg_user="$(printf "${C_CYAN}${BOLD}${user}${RST}")"
else
  seg_user="$(printf "${C_RED}login info unavailable${RST}")"
fi
seg_model="$(printf "${C_MAGENTA}${BOLD}◈ ${model}${RST}")"

seg_dir=""
[ -n "$folder" ] && seg_dir="$(printf "${C_BLUE}${BOLD}⌂ ${folder}${RST}")"

seg_branch=""
[ -n "$branch" ] && seg_branch="$(printf "${C_GREEN}⎇ ${branch}${RST}")"

seg_effort=""
if [ -n "$effort" ]; then
  effort_upper=$(echo "$effort" | tr '[:lower:]' '[:upper:]')
  seg_effort="$(printf "${C_YELLOW}$(effort_icon "$effort") ${effort_upper}${RST}")"
fi

seg_ctx=""
if [ -n "$ctx_used" ]; then
  ctx_int=$(printf '%.0f' "$ctx_used")
  ctx_disp=$(printf '%.2f' "$ctx_used")
  c=$(pct_color "$ctx_int")
  seg_ctx="$(printf "${C_WHITE}CTX${RST} ${c}$(render_bar "$ctx_int" 12)${RST} ${c}${ctx_disp}%%${RST}")"
fi

seg_5h=""
if [ -n "$five_pct" ]; then
  five_int=$(printf '%.0f' "$five_pct")
  five_disp=$(printf '%.2f' "$five_pct")
  c=$(pct_color "$five_int")
  r=$(format_reset "$five_reset")
  seg_5h="$(printf "${C_WHITE}5h${RST} ${c}$(render_bar "$five_int" 8)${RST} ${c}${five_disp}%%${RST}")"
  [ -n "$r" ] && seg_5h+="$(printf " ${C_GRAY}↺${r}${RST}")"
fi

seg_7d=""
if [ -n "$week_pct" ]; then
  week_int=$(printf '%.0f' "$week_pct")
  week_disp=$(printf '%.2f' "$week_pct")
  c=$(pct_color "$week_int")
  r=$(format_reset "$week_reset")
  seg_7d="$(printf "${C_WHITE}7d${RST} ${c}$(render_bar "$week_int" 8)${RST} ${c}${week_disp}%%${RST}")"
  [ -n "$r" ] && seg_7d+="$(printf " ${C_GRAY}↺${r}${RST}")"
fi

seg_cost=""
if [ -n "$cost" ]; then
  cost_disp=$(printf '%.2f' "$cost")
  seg_cost="$(printf "${C_GREEN}\$${cost_disp}${RST}")"
fi

seg_lines=""
if [ -n "$lines_added" ] || [ -n "$lines_removed" ]; then
  seg_lines="$(printf "${C_GREEN}+${lines_added:-0}${RST} ${C_RED}-${lines_removed:-0}${RST}")"
fi

seg_dur=""
if [ -n "$dur_ms" ]; then
  dv=$(format_dur "$dur_ms")
  [ -n "$dv" ] && seg_dur="$(printf "${C_GRAY}⏱${dv}${RST}")"
fi

# --- Responsive layout ---
sep="  ${DOT}  "

# Session stats group (cost · lines · duration)
seg_stats=""
for p in "$seg_cost" "$seg_lines" "$seg_dur"; do
  [ -z "$p" ] && continue
  [ -n "$seg_stats" ] && seg_stats+="${sep}"
  seg_stats+="$p"
done

if (( cols >= 120 )); then
  line=" ${seg_user}"
  [ -n "$seg_dir" ] && line+="${sep}${seg_dir}"
  [ -n "$seg_branch" ] && line+="${sep}${seg_branch}"
  line+="${sep}${seg_model}"
  [ -n "$seg_effort" ] && line+="${sep}${seg_effort}"
  [ -n "$seg_ctx" ] && line+="${sep}${seg_ctx}"
  [ -n "$seg_5h" ] && line+="${sep}${seg_5h}"
  [ -n "$seg_7d" ] && line+="${sep}${seg_7d}"
  [ -n "$seg_stats" ] && line+="${sep}${seg_stats}"
  printf '%b\n' "$line"

elif (( cols >= 80 )); then
  line1=" ${seg_user}"
  [ -n "$seg_dir" ] && line1+="${sep}${seg_dir}"
  [ -n "$seg_branch" ] && line1+="${sep}${seg_branch}"
  line1+="${sep}${seg_model}"
  [ -n "$seg_effort" ] && line1+="${sep}${seg_effort}"
  [ -n "$seg_ctx" ] && line1+="${sep}${seg_ctx}"
  printf '%b\n' "$line1"
  limits=""
  [ -n "$seg_5h" ] && limits+=" ${seg_5h}"
  [ -n "$seg_7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_7d}"; }
  [ -n "$seg_stats" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_stats}"; }
  [ -n "$limits" ] && printf '%b\n' " ${limits}"

else
  line1=" ${seg_user}"
  [ -n "$seg_dir" ] && line1+="${sep}${seg_dir}"
  [ -n "$seg_branch" ] && line1+="${sep}${seg_branch}"
  printf '%b\n' "$line1"
  line2=" ${seg_model}"
  [ -n "$seg_effort" ] && line2+="${sep}${seg_effort}"
  printf '%b\n' "$line2"
  [ -n "$seg_ctx" ] && printf '%b\n' " ${seg_ctx}"
  limits=""
  [ -n "$seg_5h" ] && limits+="${seg_5h}"
  [ -n "$seg_7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_7d}"; }
  [ -n "$limits" ] && printf '%b\n' " ${limits}"
  [ -n "$seg_stats" ] && printf '%b\n' " ${seg_stats}"
fi
```

### Step 2: Add statusLine and effortLevel to settings.json

Read `~/.claude/settings.json` and add/update the following two fields while preserving existing settings:

```json
{
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-command.sh"
  },
  "effortLevel": "<argument or 'max'>"
}
```

- If `$ARGUMENTS` is provided (low/medium/high/xhigh/max), use it as the effortLevel value
- Otherwise default to "max"
- Create settings.json if it doesn't exist
- Never modify other existing settings (hooks, env, etc.)
- `effortLevel` sets the **default** effort for new sessions. The status line itself shows the **live** effort from each session's payload, so it stays correct even when you change effort mid-session.

### Step 3: Completion Message

After setup, display the following:

```
Status line setup complete!

Wide (>=120):
 aaron  ·  ⌂ kweiza-cc-plugins  ·  ⎇ main  ·  ◈ Opus 4.8 (1M context)  ·  🔥⚡⚡⚡ MAX  ·  CTX ████▓░░░░░░░ 38.27%  ·  5h ████░░░░ 52.40% ↺1h23m  ·  7d █▓░░░░░░ 21.08% ↺6d  ·  $0.42  ·  +120 -34  ·  ⏱1h12m

Medium (80-119):
 aaron  ·  ⌂ kweiza-cc-plugins  ·  ⎇ main  ·  ◈ Opus 4.8 (1M context)  ·  🔥⚡⚡⚡ MAX  ·  CTX ████▓░░░░░░░ 38.27%
 5h ████░░░░ 52.40% ↺1h23m  ·  7d █▓░░░░░░ 21.08% ↺6d  ·  $0.42  ·  +120 -34  ·  ⏱1h12m

Narrow (<80):
 aaron  ·  ⌂ kweiza-cc-plugins  ·  ⎇ main
 ◈ Opus 4.8 (1M context)  ·  🔥⚡⚡⚡ MAX
 CTX ████▓░░░░░░░ 38.27%
 5h ████░░░░ 52.40% ↺1h23m  ·  7d █▓░░░░░░ 21.08% ↺6d
 $0.42  ·  +120 -34  ·  ⏱1h12m
```

### Notes

- Effort level is read **live** from the status line payload (`.effort.level`), so it reflects the current session — including mid-session `/effort` changes and `max`/`xhigh` — with no manual settings edits
- If the model exposes no effort parameter, the script falls back to the `effortLevel` field in settings.json, and hides the effort segment entirely when neither is available
- `effortLevel` in settings.json now only sets the **default** effort for new sessions; it no longer has to match the live display
- Workspace folder is the project root (`.workspace.project_dir`); git branch is read live via `git branch --show-current` and is hidden outside a repo or on a detached HEAD
- Session stats come from the `.cost` block: cost (`$`, 2 decimals), lines changed (`+added -removed`), elapsed time (`⏱`); each segment is hidden when its field is absent
- Effort icons: low `⚡` · medium `⚡⚡` · high `⚡⚡⚡` · xhigh `⚡⚡⚡⚡` · max `🔥⚡⚡⚡`
- Usage percentages (CTX / 5h / 7d) show 2 decimals, rounded at the 3rd place (e.g. `38.27%`); the bars and colors still use the integer value
- Reset countdowns over 24h show whole days only (e.g. `↺6d`); under 24h they show `↺1h23m` / `↺12m`
- Requires `jq` to be installed
