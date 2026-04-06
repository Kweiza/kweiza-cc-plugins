---
name: grafik-bar
description: "Claude Code status line setup - model, effort, context window, rate limits with responsive graphical bars"
user-invocable: true
argument-hint: "[optional: effort level - low/medium/high/max, default: high]"
---

# Setup Status Line

Claude Code status line을 그래픽 바와 함께 설정합니다.

## 표시 항목

- Model name (magenta bold)
- Reasoning effort level (thunder icons)
- Context window usage (12-cell progress bar)
- 5-hour session limit (8-cell bar + reset countdown)
- 7-day weekly limit (8-cell bar + reset countdown)

## 색상 임계값

- 0-39%: Green
- 40-69%: Cyan
- 70-89%: Yellow
- 90-100%: Red

## 반응형 레이아웃

- Wide (>=120 cols): 한 줄
- Medium (80-119): 두 줄
- Narrow (<80): 세 줄

## Setup Instructions

아래 두 가지를 수행하세요:

### Step 1: statusline-command.sh 생성

`~/.claude/statusline-command.sh` 파일을 아래 내용으로 생성하세요:

```bash
#!/usr/bin/env bash
# Claude Code status line - responsive graphical display

input=$(cat)

# --- Terminal width ---
cols=$(tput cols </dev/tty 2>/dev/null || stty size </dev/tty 2>/dev/null | awk '{print $2}' || echo 120)

# --- Extract fields ---
model=$(echo "$input" | jq -r '.model.display_name // "Unknown Model"')
effort=$(jq -r '.effortLevel // "normal"' ~/.claude/settings.json 2>/dev/null || echo "normal")
ctx_used=$(echo "$input" | jq -r '.context_window.used_percentage // empty')
five_pct=$(echo "$input" | jq -r '.rate_limits.five_hour.used_percentage // empty')
five_reset=$(echo "$input" | jq -r '.rate_limits.five_hour.resets_at // empty')
week_pct=$(echo "$input" | jq -r '.rate_limits.seven_day.used_percentage // empty')
week_reset=$(echo "$input" | jq -r '.rate_limits.seven_day.resets_at // empty')

# --- ANSI colors ---
RST='\033[0m'
BOLD='\033[1m'
C_CYAN='\033[36m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_RED='\033[31m'
C_MAGENTA='\033[35m'
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
  local epoch=$1 now delta h m
  [ -z "$epoch" ] && return
  now=$(date +%s)
  delta=$(( epoch - now ))
  (( delta <= 0 )) && printf 'now' && return
  h=$(( delta / 3600 )); m=$(( (delta % 3600) / 60 ))
  (( h > 0 )) && printf '%dh%02dm' "$h" "$m" || printf '%dm' "$m"
}

effort_icon() {
  case "$1" in
    max)    printf '🔥⚡⚡⚡' ;;
    high)   printf '⚡⚡⚡' ;;
    medium) printf '⚡⚡' ;;
    low)    printf '⚡' ;;
    *)      printf '⚡' ;;
  esac
}

# --- Build segments ---
effort_upper=$(echo "$effort" | tr '[:lower:]' '[:upper:]')
seg_model="$(printf "${C_MAGENTA}${BOLD}◈ ${model}${RST}")"
seg_effort="$(printf "${C_YELLOW}$(effort_icon "$effort") ${effort_upper}${RST}")"

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

# --- Responsive layout ---
sep="  ${DOT}  "

if (( cols >= 120 )); then
  line=" ${seg_model}${sep}${seg_effort}"
  [ -n "$seg_ctx" ] && line+="${sep}${seg_ctx}"
  [ -n "$seg_5h" ] && line+="${sep}${seg_5h}"
  [ -n "$seg_7d" ] && line+="${sep}${seg_7d}"
  printf '%b\n' "$line"

elif (( cols >= 80 )); then
  printf '%b\n' " ${seg_model}${sep}${seg_effort}$([ -n "$seg_ctx" ] && printf "${sep}${seg_ctx}")"
  limits=""
  [ -n "$seg_5h" ] && limits+=" ${seg_5h}"
  [ -n "$seg_7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_7d}"; }
  [ -n "$limits" ] && printf '%b\n' " ${limits}"

else
  printf '%b\n' " ${seg_model}${sep}${seg_effort}"
  [ -n "$seg_ctx" ] && printf '%b\n' " ${seg_ctx}"
  limits=""
  [ -n "$seg_5h" ] && limits+="${seg_5h}"
  [ -n "$seg_7d" ] && { [ -n "$limits" ] && limits+="${sep}"; limits+="${seg_7d}"; }
  [ -n "$limits" ] && printf '%b\n' " ${limits}"
fi
```

### Step 2: settings.json에 statusLine 및 effortLevel 추가

`~/.claude/settings.json`을 읽어서 기존 설정을 유지하면서 아래 두 필드를 추가/업데이트하세요:

```json
{
  "statusLine": {
    "type": "command",
    "command": "bash ~/.claude/statusline-command.sh"
  },
  "effortLevel": "<argument or 'high'>"
}
```

- `$ARGUMENTS`가 있으면 (low/medium/high/max) 해당 값을 effortLevel로 설정
- 없으면 기본값 "high" 사용
- settings.json이 없으면 새로 생성
- 기존 hooks, env 등 다른 설정은 절대 건드리지 않을 것

### Step 3: 완료 메시지

설정 완료 후 아래처럼 보고:

```
Status line 설정 완료!

Wide (>=120):
 ◈ Opus 4.6 (1M context)  ·  ⚡⚡⚡ HIGH  ·  CTX ████▓░░░░░░░ 38%  ·  5h ████░░░░ 52% ↺1h23m  ·  7d ██░░░░░░ 21% ↺4d11h

Medium (80-119):
 ◈ Opus 4.6 (1M context)  ·  ⚡⚡⚡ HIGH  ·  CTX ████▓░░░░░░░ 38%
 5h ████░░░░ 52% ↺1h23m  ·  7d ██░░░░░░ 21% ↺4d11h

Narrow (<80):
 ◈ Opus 4.6 (1M context)  ·  ⚡⚡⚡ HIGH
 CTX ████▓░░░░░░░ 38%
 5h ████░░░░ 52% ↺1h23m  ·  7d ██░░░░░░ 21% ↺4d11h

Note: /effort max는 settings.json에 기록되지 않으므로 수동 설정 필요
```

### 참고 사항

- effort level은 settings.json의 `effortLevel` 필드에서 읽음
- `/effort low|medium|high`는 settings.json을 자동 업데이트하므로 status line에 즉시 반영
- `/effort max`는 세션 전용이라 settings.json에 기록되지 않음 (Claude Code 제한사항)
- max를 표시하려면 settings.json의 effortLevel을 직접 "max"로 변경 필요
- `jq`가 설치되어 있어야 함
