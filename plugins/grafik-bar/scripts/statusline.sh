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
# COLUMNS first, because it is the only probe that actually works here. A status
# line runs with no controlling terminal: `/dev/tty` cannot be opened at all, so
# `tput cols </dev/tty` and `stty size </dev/tty` fail at the *redirection* step —
# before the command runs, which is also why stubbing tput never had any effect.
# Measured on a live session: both come back empty every time, and each render
# leaked two "No such device or address" lines to stderr. Claude Code meanwhile
# exports COLUMNS with the live width, and it tracks window resizes.
#
# The tty probes stay as a fallback for hosts that hand this script a terminal but
# no COLUMNS — guarded by an open test, so they run only where they can work.
cols="${COLUMNS:-}"
if [ -z "$cols" ] && (exec 3</dev/tty) 2>/dev/null; then
  cols=$(tput cols </dev/tty 2>/dev/null)
  [ -z "$cols" ] && cols=$(stty size </dev/tty 2>/dev/null | awk '{print $2}')
fi
# Check the value, not the exit status: a pipeline's status is awk's, and awk
# succeeds on empty input, so a `|| echo 120` tail here would never run.
case "$cols" in ''|*[!0-9]*) cols=120 ;; esac
# A floor, so a bogus 0 cannot make every segment its own line.
if (( cols < 20 )); then cols=20; fi

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
# `$'…'` so these hold real escape bytes from the moment they are assigned. The
# old form kept them as the literal text `\033[36m` and relied on every use site
# passing them through `printf` — which also made every segment's *content* part
# of a printf format string, so a `%` in an email or a branch name would eat the
# next argument. Real bytes here means the layout below can concatenate plainly
# and print with `%s`.
RST=$'\033[0m'
BOLD=$'\033[1m'
C_CYAN=$'\033[36m'
C_GREEN=$'\033[32m'
C_YELLOW=$'\033[33m'
C_RED=$'\033[31m'
C_MAGENTA=$'\033[35m'
C_BLUE=$'\033[34m'
C_WHITE=$'\033[37m'
C_GRAY=$'\033[90m'

# The separator, in both forms the layout needs: what it looks like, and how many
# columns it costs.
sep="  ${C_GRAY}·${RST}  "
sep_txt="  ·  "
sep_w=5

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
# Every segment is pushed as a PAIR: the plain text, which is what the width
# measurement reads, and the colored text, which is what gets printed. Carrying
# both is the whole point — it lets the layout ask "how wide is this actually?"
# instead of guessing. The guess is what put a 116-column line into an 89-column
# window: the branch thresholds (`cols >= 80`, `cols >= 120`) were round numbers
# picked without ever measuring what the branches emit (they emit 116 and 220).
# And no constant could have been right, because the content is variable width —
# a longer email or branch name moves it.
segs_txt=()
segs_out=()
push_seg() {
  [ -z "$2" ] && return 0
  segs_txt+=("$1")
  segs_out+=("$2")
  return 0
}

if [ -n "$user" ]; then
  push_seg "$user" "${C_CYAN}${BOLD}${user}${RST}"
else
  push_seg "login info unavailable" "${C_RED}login info unavailable${RST}"
fi

[ -n "$folder" ] && push_seg "⌂ ${folder}" "${C_BLUE}${BOLD}⌂ ${folder}${RST}"
[ -n "$branch" ] && push_seg "⎇ ${branch}" "${C_GREEN}⎇ ${branch}${RST}"

push_seg "◈ ${model}" "${C_MAGENTA}${BOLD}◈ ${model}${RST}"

if [ -n "$effort" ]; then
  effort_upper=$(echo "$effort" | tr '[:lower:]' '[:upper:]')
  effort_ico=$(effort_icon "$effort")
  push_seg "${effort_ico} ${effort_upper}" "${C_YELLOW}${effort_ico} ${effort_upper}${RST}"
fi

if [ -n "$ctx_used" ]; then
  ctx_int=$(printf '%.0f' "$ctx_used")
  c=$(pct_color "$ctx_int")
  bar=$(render_bar "$ctx_int" 12)
  push_seg "CTX ${bar} ${ctx_int}%" "${C_WHITE}CTX${RST} ${c}${bar}${RST} ${c}${ctx_int}%${RST}"
fi

# 5h / 7d / F7d are the same shape three times over — one place to build them, so
# the plain text and the colored text cannot drift apart between the three.
push_limit() {
  local label=$1 pct=$2 reset=$3 i c bar r plain out
  [ -z "$pct" ] && return 0
  i=$(printf '%.0f' "$pct")
  c=$(pct_color "$i")
  bar=$(render_bar "$i" 8)
  r=$(format_reset "$reset")
  plain="${label} ${bar} ${i}%"
  out="${C_WHITE}${label}${RST} ${c}${bar}${RST} ${c}${i}%${RST}"
  if [ -n "$r" ]; then
    plain+=" ↺${r}"
    out+=" ${C_GRAY}↺${r}${RST}"
  fi
  push_seg "$plain" "$out"
  return 0
}
push_limit "5h" "$five_pct" "$five_reset"
push_limit "7d" "$week_pct" "$week_reset"
push_limit "F7d" "$fable_pct" "$fable_reset"

# Session stats stay one segment: cost, lines and duration read together, and
# splitting them across a line break would be worse than moving them as a block.
stats_txt=""
stats_out=""
add_stat() {
  [ -z "$2" ] && return 0
  if [ -n "$stats_out" ]; then
    stats_txt+="$sep_txt"
    stats_out+="$sep"
  fi
  stats_txt+="$1"
  stats_out+="$2"
  return 0
}
if [ -n "$cost" ]; then
  cost_disp=$(printf '%.2f' "$cost")
  add_stat "\$${cost_disp}" "${C_GREEN}\$${cost_disp}${RST}"
fi
if [ -n "$lines_added" ] || [ -n "$lines_removed" ]; then
  add_stat "+${lines_added:-0} -${lines_removed:-0}" \
    "${C_GREEN}+${lines_added:-0}${RST} ${C_RED}-${lines_removed:-0}${RST}"
fi
if [ -n "$dur_ms" ]; then
  dv=$(format_dur "$dur_ms")
  [ -n "$dv" ] && add_stat "⏱${dv}" "${C_GRAY}⏱${dv}${RST}"
fi
push_seg "$stats_txt" "$stats_out"

# --- Measure ---
# One jq pass over all the plain texts. jq is already this script's hard
# dependency and it decodes UTF-8 into codepoints, which is what a width rule
# needs — `${#s}` counts characters, and a character is not a column: ⚡ and CJK
# take two. Getting that wrong is not cosmetic here, it is the difference between
# a line that fits and a line the terminal folds.
seg_w=()
if (( ${#segs_txt[@]} > 0 )); then
  # -r matters: without it jq quotes the string and the first and last tokens come
  # back as `"22` and `30"`. Those are not numbers, `(( ))` on them just evaluates
  # false, and the effect is invisible — the first and last segments silently stop
  # joining any line. Measured, not reasoned about.
  measured=$(printf '%s\n' "${segs_txt[@]}" | jq -Rrn '
    def w:
      explode
      | map(
          . as $c
          | if   $c < 32                          then 0   # control: no width
            elif $c >= 4352   and $c <= 4447      then 2   # 1100..115F  Hangul jamo
            elif $c >= 11904  and $c <= 12350     then 2   # 2E80..303E  CJK radicals/symbols
            elif $c >= 12353  and $c <= 13311     then 2   # 3041..33FF  kana, compat
            elif $c >= 13312  and $c <= 19903     then 2   # 3400..4DBF  CJK ext A
            elif $c >= 19968  and $c <= 40959     then 2   # 4E00..9FFF  CJK unified
            elif $c >= 40960  and $c <= 42191     then 2   # A000..A4CF  Yi
            elif $c >= 44032  and $c <= 55203     then 2   # AC00..D7A3  Hangul syllables
            elif $c >= 63744  and $c <= 64255     then 2   # F900..FAFF  CJK compat
            elif $c >= 65072  and $c <= 65135     then 2   # FE30..FE6F  CJK compat forms
            elif $c >= 65280  and $c <= 65376     then 2   # FF00..FF60  fullwidth
            elif $c >= 65504  and $c <= 65510     then 2   # FFE0..FFE6  fullwidth signs
            elif $c >= 127744 and $c <= 129791    then 2   # 1F300..1FAFF emoji (🔥)
            elif $c >= 131072 and $c <= 262141    then 2   # 20000..3FFFD CJK ext B+
            # Emoji-presentation singles. 26A1 (⚡) is this script effort icon.
            elif ([9889,8986,8987,9200,9203,9725,9726,9748,9749,11035,11036,11088,11093]
                  | index($c))                    then 2
            else 1 end)
      | add // 0;
    [inputs | w] | map(tostring) | join(" ")' 2>/dev/null)
  read -r -a seg_w <<< "$measured"
fi

# --- Lay out ---
# Greedy: fill the current line while the next segment still fits, otherwise start
# a new one. Every line begins with one leading space, so that column is spent up
# front. A segment wider than the whole window goes on its own line and overflows
# — an email cannot be split, and that is the only overflow left.
avail=$(( cols - 1 ))
if (( avail < 1 )); then avail=1; fi

line_out=""
line_w=0
for i in "${!segs_out[@]}"; do
  # Fall back to the character count if the measurement is missing or is not a
  # number. It undercounts wide characters, never overcounts, so the worst case is
  # the old behaviour for that one segment rather than a silent collapse.
  #
  # The digit check is not paranoia: a non-numeric token here does not fail, it
  # makes `(( ))` quietly evaluate false, and the only symptom is a segment that
  # stops sharing a line. That is exactly how the missing `-r` above hid.
  w=${seg_w[i]:-}
  case "$w" in ''|*[!0-9]*) w=${#segs_txt[i]} ;; esac
  if [ -z "$line_out" ]; then
    line_out="${segs_out[i]}"
    line_w=$w
  elif (( line_w + sep_w + w <= avail )); then
    line_out+="${sep}${segs_out[i]}"
    line_w=$(( line_w + sep_w + w ))
  else
    printf ' %s\n' "$line_out"
    line_out="${segs_out[i]}"
    line_w=$w
  fi
done
[ -n "$line_out" ] && printf ' %s\n' "$line_out"

# The loop above ends in `[ -n "$x" ] && printf …`, so with nothing to print the
# script's status would be the failed test's — exit 1 on a perfectly good render.
# A status line has no way to report a failure, so pin the status here.
exit 0
