#!/usr/bin/env bash
# Claude Code status line - responsive graphical display

input=$(cat)

# Every field below is read with jq. Without a working one this renders as an
# empty shell and floods stderr with "command not found" — the user sees a broken
# line and no stated cause, because stderr goes nowhere. `command -v jq` is not
# enough: a jq that exists but cannot run (broken install, wrong arch, no execute
# bit) lands in exactly the same place. So run it, and say so on stdout, which is
# the only channel a status line has.
if ! printf '{}' | jq -e . >/dev/null 2>&1; then
  printf ' grafik-bar: jq not found — the status line needs jq\n'
  exit 0
fi

# --- Terminal width ---
# Both probes need a controlling tty, and there is none under a hook runner, an
# IDE, or a remote session. The `|| echo 120` that used to close this pipeline
# never ran: a pipeline's status is awk's, and awk succeeds on empty input. So
# cols came out EMPTY and every tty-less environment silently fell through to the
# narrowest layout. Check the value, not the exit status.
cols=$(tput cols </dev/tty 2>/dev/null)
[ -z "$cols" ] && cols=$(stty size </dev/tty 2>/dev/null | awk '{print $2}')
# Neither probe can work without a controlling tty, so honour COLUMNS when the
# caller sets it. That is also the only handle a test has on the layout branches:
# /dev/tty cannot be handed to a child process, so a stub tput is never even run.
[ -z "$cols" ] && cols="${COLUMNS:-}"
case "$cols" in ''|*[!0-9]*) cols=120 ;; esac

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

# Fable weekly usage: not in the statusline stdin payload — only the claude.ai
# usage API exposes it, as a weekly_scoped limit with scope.model "Fable".
# Cache the response and refresh in the background so rendering never blocks.
FABLE_CACHE="${TMPDIR:-/tmp}/grafik-bar-usage-$(id -u).json"
FABLE_CACHE_TTL=300
fetch_usage() {
  local tok
  tok=$(jq -r '.claudeAiOauth.accessToken // empty' ~/.claude/.credentials.json 2>/dev/null)
  [ -z "$tok" ] && return 1
  curl -sf -m 4 "https://api.anthropic.com/api/oauth/usage" \
    -H "Authorization: Bearer $tok" \
    -H "anthropic-beta: oauth-2025-04-20" > "${FABLE_CACHE}.tmp" 2>/dev/null \
    && mv "${FABLE_CACHE}.tmp" "$FABLE_CACHE"
}
cache_age=$(( $(date +%s) - $(stat -c %Y "$FABLE_CACHE" 2>/dev/null || echo 0) ))
if (( cache_age > FABLE_CACHE_TTL )); then
  if [ -s "$FABLE_CACHE" ]; then
    ( fetch_usage & ) >/dev/null 2>&1
  else
    fetch_usage >/dev/null 2>&1
  fi
fi
fable_pct="" fable_reset=""
if [ -s "$FABLE_CACHE" ]; then
  fable_pct=$(jq -r '[.limits[]? | select(.kind == "weekly_scoped"
    and ((.scope.model.display_name // "") | test("fable"; "i")))]
    | first | .percent // empty' "$FABLE_CACHE" 2>/dev/null)
  if [ -n "$fable_pct" ]; then
    fable_reset_iso=$(jq -r '[.limits[]? | select(.kind == "weekly_scoped"
      and ((.scope.model.display_name // "") | test("fable"; "i")))]
      | first | .resets_at // empty' "$FABLE_CACHE" 2>/dev/null)
    [ -n "$fable_reset_iso" ] && fable_reset=$(date -d "$fable_reset_iso" +%s 2>/dev/null)
  fi
fi

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
  if (( d > 0 )); then printf '%dd%dh%02dm' "$d" "$h" "$m"
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
  c=$(pct_color "$ctx_int")
  seg_ctx="$(printf "${C_WHITE}CTX${RST} ${c}$(render_bar "$ctx_int" 12)${RST} ${c}${ctx_int}%%${RST}")"
fi

seg_5h=""
if [ -n "$five_pct" ]; then
  five_int=$(printf '%.0f' "$five_pct")
  c=$(pct_color "$five_int")
  r=$(format_reset "$five_reset")
  seg_5h="$(printf "${C_WHITE}5h${RST} ${c}$(render_bar "$five_int" 8)${RST} ${c}${five_int}%%${RST}")"
  [ -n "$r" ] && seg_5h+="$(printf " ${C_GRAY}↺${r}${RST}")"
fi

seg_7d=""
if [ -n "$week_pct" ]; then
  week_int=$(printf '%.0f' "$week_pct")
  c=$(pct_color "$week_int")
  r=$(format_reset "$week_reset")
  seg_7d="$(printf "${C_WHITE}7d${RST} ${c}$(render_bar "$week_int" 8)${RST} ${c}${week_int}%%${RST}")"
  [ -n "$r" ] && seg_7d+="$(printf " ${C_GRAY}↺${r}${RST}")"
fi

seg_f7d=""
if [ -n "$fable_pct" ]; then
  fable_int=$(printf '%.0f' "$fable_pct")
  c=$(pct_color "$fable_int")
  r=$(format_reset "$fable_reset")
  seg_f7d="$(printf "${C_WHITE}F7d${RST} ${c}$(render_bar "$fable_int" 8)${RST} ${c}${fable_int}%%${RST}")"
  [ -n "$r" ] && seg_f7d+="$(printf " ${C_GRAY}↺${r}${RST}")"
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
  [ -n "$seg_f7d" ] && line+="${sep}${seg_f7d}"
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
  [ -n "$seg_f7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_f7d}"; }
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
  [ -n "$seg_f7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_f7d}"; }
  [ -n "$limits" ] && printf '%b\n' " ${limits}"
  [ -n "$seg_stats" ] && printf '%b\n' " ${seg_stats}"
fi

# Each layout branch ends in `[ -n "$x" ] && printf …`, so with an empty trailing
# segment the script's status was the failed test's — exit 1 on a perfectly good
# render. A status line has no way to report a failure, so pin the status here.
exit 0
