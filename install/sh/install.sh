#!/usr/bin/env bash
# 接上AI · Mac / Linux 安装脚本草稿（本地，不部署）
#
# 正式形态：curl -fsSL https://jieshangai.com/i/{token} | bash
# 管道本身看不到网址。服务端接入前，助手种类和短期凭证从下面几处读取
# （先到先用，空着的才用下一处）：
#   1. 环境变量 RELAY_AGENT、RELAY_TOKEN
#   2. 第一个参数：完整网址，或查询串 agent=claude&token=...
#   3. RELAY_INSTALL_URL
#   4. RELAY_QUERY，其次 QUERY_STRING
# 写入配置的内容默认就是这份短期凭证（可假）。若已设置 RELAY_SECRET，则改写它。
# 网关 /i/{token} 下发时会注入 RELAY_CLAIM=1 与 RELAY_SITE_ORIGIN：脚本向
# POST /api/install/claim 领取一次连接凭证，并向 /api/install/progress 回报进度
# （beat 1..5 / done / failed），最后用 GET /v1/models 测一次连接。
# 这些值都不会打到屏幕上。
set +x
if [ -z "${BASH_VERSION:-}" ]; then
  printf '%s\n' '请用 bash 打开命令窗口再粘贴一次。'
  exit 1
fi
set -euo pipefail
umask 077

CLAUDE_INSTALL_URL='https://claude.ai/install.sh'
CODEX_INSTALL_URL='https://chatgpt.com/codex/install.sh'
PROVIDER_ID='jieshangai'
CODEX_ENV_NAME='JIESHANGAI_CODEX_KEY'
DEFAULT_CODEX_MODEL='gpt-5.6-sol'
MARKER_START='# jieshangai-provider-start'
MARKER_END='# jieshangai-provider-end'
RC_START='# jieshangai-connection-start'
RC_END='# jieshangai-connection-end'
MODEL_TAG='# jieshangai-model'
CATALOG_TAG='# jieshangai-catalog'
MANIFEST_HEADER='# jieshangai backup manifest v1'
REPORT_TOKEN=''
REPORT_ORIGIN=''

say() { printf '%s\n' "$*"; }

conf_root() {
  if [ -n "${RELAY_HOME:-}" ]; then
    printf '%s' "$RELAY_HOME"
  else
    printf '%s' "${HOME:?}"
  fi
}

log_code() {
  local root code
  root=$(conf_root)
  code=$1
  mkdir -p "$root/.jieshangai"
  printf '%s\n' "$code" >"$root/.jieshangai/install-error.log"
  chmod 600 "$root/.jieshangai/install-error.log" 2>/dev/null || true
  report '' failed "$code"
}

claim_mode() { [ "${RELAY_CLAIM:-}" = '1' ]; }

# report <beat> <status> [error]: best effort, never fails the install.
report() {
  claim_mode || return 0
  [ -n "$REPORT_TOKEN" ] && [ -n "$REPORT_ORIGIN" ] || return 0
  command -v curl >/dev/null 2>&1 || return 0
  local beat=${1:-} status=${2:-running} err=${3:-} body
  case "$err" in *[!A-Za-z0-9_-]*) err='INSTALL_FAILED' ;; esac
  body="{\"token\":\"$REPORT_TOKEN\",\"status\":\"$status\",\"error\":\"$err\""
  if [ -n "$beat" ]; then
    body="$body,\"beat\":$beat"
  fi
  body="$body}"
  curl -fsS -m 10 -X POST -H 'Content-Type: application/json' \
    --data "$body" "$REPORT_ORIGIN/api/install/progress" >/dev/null 2>&1 || true
}

# claim_secret <origin> <token>: prints the relay credential once.
claim_secret() {
  local origin=$1 token=$2 resp key
  command -v curl >/dev/null 2>&1 || return 1
  resp=$(curl -fsS -m 20 -X POST -H 'Content-Type: application/json' \
    --data "{\"token\":\"$token\"}" "$origin/api/install/claim" 2>/dev/null) || return 1
  key=$(printf '%s' "$resp" | tr -d '\n' | sed -n 's/.*"key"[[:space:]]*:[[:space:]]*"\([A-Za-z0-9_-]*\)".*/\1/p')
  [ -n "$key" ] || return 1
  printf '%s' "$key"
}

# test_connection <origin> <secret>
test_connection() {
  curl -fsS -m 20 -o /dev/null -H "Authorization: Bearer $2" "$1/v1/models" 2>/dev/null
}

# fetch_install_catalog <origin> <secret> <agent> <root>: best-effort live catalog fetch
# Sets CATALOG_STATUS ('ok' | 'withdrawn' | ''), CATALOG_CLAUDE_PRIMARY, CATALOG_CLAUDE_SONNET,
# CATALOG_CLAUDE_OPUS, CATALOG_CLAUDE_HAIKU, CATALOG_CODEX_MODEL, CATALOG_CODEX_PATH.
# Network failures leave CATALOG_STATUS='' (preserve existing config); explicit server
# withdrawal (enabled=false or empty models) sets CATALOG_STATUS='withdrawn' so stale
# managed catalogs/slots are cleared on reinstall.
CATALOG_STATUS=''
CATALOG_CLAUDE_PRIMARY=''
CATALOG_CLAUDE_SONNET=''
CATALOG_CLAUDE_OPUS=''
CATALOG_CLAUDE_HAIKU=''
CATALOG_CODEX_MODEL=''
CATALOG_CODEX_PATH=''

fetch_install_catalog() {
  local origin=$1 secret=$2 agent=$3 root=$4 raw='' parsed=''
  claim_mode || return 0
  have_cmd curl && have_cmd python3 || return 0
  raw=$(curl -fsS -m 10 -H "Authorization: Bearer $secret" "$origin/api/install/catalog" 2>/dev/null) || return 0
  [ -n "$raw" ] || return 0
  parsed=$(RELAY_CATALOG_RAW="$raw" RELAY_CATALOG_AGENT="$agent" RELAY_CATALOG_ROOT="$root" python3 - <<'PY' 2>/dev/null
import json, os, re, sys

raw = os.environ.get("RELAY_CATALOG_RAW", "")
agent = os.environ.get("RELAY_CATALOG_AGENT", "")
root = os.environ.get("RELAY_CATALOG_ROOT", "")
re_model = re.compile(r"^[A-Za-z0-9._:/-]+$")

def clean(v):
    s = (v or "").strip() if isinstance(v, str) else ""
    return s if s and re_model.match(s) else ""

try:
    payload = json.loads(raw)
except Exception:
    sys.exit(0)

if not isinstance(payload, dict) or not payload.get("success"):
    sys.exit(0)
data = payload.get("data")
if not isinstance(data, dict):
    sys.exit(0)
models = data.get("models")
if not data.get("enabled") or not isinstance(models, list) or not models:
    print("CATALOG_STATUS=withdrawn")
    sys.exit(0)

print("CATALOG_STATUS=ok")
if agent == "claude":
    slots = data.get("claude_slots") if isinstance(data.get("claude_slots"), dict) else {}
    primary = clean(data.get("default_model")) or clean(slots.get("primary"))
    if not primary:
        primary = next(filter(None, (clean(item.get("id")) for item in models if isinstance(item, dict))), "")
    sonnet = clean(slots.get("sonnet")) or primary
    opus = clean(slots.get("opus")) or sonnet or primary
    haiku = clean(slots.get("haiku")) or sonnet or primary
    if primary and not primary.lower().startswith("claude"):
        sonnet = opus = haiku = primary
    # The aliases apply to named selections. Claude Code also needs the startup model.
    print(f"CLAUDE_PRIMARY={primary}")
    print(f"CLAUDE_SONNET={sonnet}")
    print(f"CLAUDE_OPUS={opus}")
    print(f"CLAUDE_HAIKU={haiku}")
elif agent == "codex":
    default_model = clean(data.get("default_model"))
    entries = []
    default_levels = [
        {"effort": "low", "description": "Faster responses with lighter reasoning"},
        {"effort": "medium", "description": "Balanced speed and reasoning depth"},
        {"effort": "high", "description": "Greater reasoning depth for complex tasks"},
    ]
    for i, item in enumerate(models):
        if not isinstance(item, dict):
            continue
        mid = clean(item.get("id"))
        if not mid:
            continue
        name = (item.get("display_name") or "").strip() if isinstance(item.get("display_name"), str) else ""
        if not name:
            name = mid
        ctx = item.get("context_window")
        if not isinstance(ctx, int) or ctx <= 0:
            ctx = 200000
        vis = (item.get("visibility") or "").strip().lower() if isinstance(item.get("visibility"), str) else ""
        if vis != "hide":
            vis = "list"
        vendor = (item.get("vendor") or "").strip() if isinstance(item.get("vendor"), str) else ""
        entry = {
            "slug": mid,
            "display_name": name,
            "default_reasoning_level": "medium",
            "supported_reasoning_levels": default_levels,
            "shell_type": "shell_command",
            "visibility": vis,
            "supported_in_api": True,
            "priority": i,
            "base_instructions": "",
            "supports_reasoning_summaries": True,
            "support_verbosity": False,
            "truncation_policy": {"mode": "tokens", "limit": 10000},
            "supports_parallel_tool_calls": True,
            "context_window": ctx,
            "experimental_supported_tools": [],
        }
        if vendor:
            entry["description"] = vendor + " via 接上AI"
        entries.append(entry)
    cat_path = ""
    if entries and root:
        site_dir = os.path.join(root, ".jieshangai")
        os.makedirs(site_dir, mode=0o700, exist_ok=True)
        cat_path = os.path.join(site_dir, "codex-catalog.json")
        tmp = cat_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump({"models": entries}, fh, ensure_ascii=False, indent=2)
            fh.write("\n")
        os.chmod(tmp, 0o600)
        os.replace(tmp, cat_path)
    print(f"CODEX_MODEL={default_model}")
    print(f"CODEX_PATH={cat_path}")
PY
  ) || return 0
  local line k v
  while IFS= read -r line; do
    k=${line%%=*}
    v=${line#*=}
    case "$k" in
      CATALOG_STATUS) CATALOG_STATUS=$v ;;
      CLAUDE_PRIMARY) CATALOG_CLAUDE_PRIMARY=$v ;;
      CLAUDE_SONNET) CATALOG_CLAUDE_SONNET=$v ;;
      CLAUDE_OPUS) CATALOG_CLAUDE_OPUS=$v ;;
      CLAUDE_HAIKU) CATALOG_CLAUDE_HAIKU=$v ;;
      CODEX_MODEL) CATALOG_CODEX_MODEL=$v ;;
      CODEX_PATH) CATALOG_CODEX_PATH=$v ;;
    esac
  done <<<"$parsed"
  if [ "$CATALOG_STATUS" = 'withdrawn' ] && [ "$agent" = 'codex' ] && [ -n "$root" ]; then
    rm -f "$root/.jieshangai/codex-catalog.json"
  fi
}

clear_log() {
  local root
  root=$(conf_root)
  rm -f "$root/.jieshangai/install-error.log"
}

trim() {
  local s=$1
  s=${s#"${s%%[![:space:]]*}"}
  s=${s%"${s##*[![:space:]]}"}
  printf '%s' "$s"
}

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

url_decode() {
  local s=${1//+/ }
  local out='' ch
  while [[ "$s" =~ ^([^%]*)%([0-9A-Fa-f]{2})(.*)$ ]]; do
    out+="${BASH_REMATCH[1]}"
    printf -v ch '%b' "\\x${BASH_REMATCH[2]}"
    out+="$ch"
    s=${BASH_REMATCH[3]}
  done
  printf '%s' "$out$s"
}

extract_query() {
  local raw=$1
  case "$raw" in
    *\?*)
      raw=${raw#*\?}
      raw=${raw%%\#*}
      printf '%s' "$raw"
      ;;
    *://*) ;;
    *=*) printf '%s' "$raw" ;;
  esac
}

extract_path_token() {
  local raw=$1 path
  case "$raw" in
    *://*)
      path=${raw%%\?*}
      path=${path%%\#*}
      case "$path" in
        */i/*)
          path=${path##*/i/}
          path=${path%%/*}
          url_decode "$path"
          ;;
      esac
      ;;
  esac
}

query_value() {
  local query=$1 want=$2 part key val
  query=${query#\?}
  [ -n "$query" ] || return 0
  set -f
  local IFS='&'
  for part in $query; do
    [ -n "$part" ] || continue
    key=${part%%=*}
    if [ "$part" = "$key" ]; then
      val=''
    else
      val=${part#*=}
    fi
    key=$(lower "$(url_decode "$key")")
    if [ "$key" = "$want" ]; then
      url_decode "$val"
      set +f
      return 0
    fi
  done
  set +f
}

fill_from_raw() {
  local raw=$1 query path_token q_agent q_token
  [ -n "$raw" ] || return 0
  query=$(extract_query "$raw")
  path_token=$(extract_path_token "$raw")
  q_agent=$(query_value "$query" agent)
  q_token=$(query_value "$query" token)
  if [ -z "$agent" ] && [ -n "$q_agent" ]; then
    agent=$(lower "$(trim "$q_agent")")
  fi
  if [ -z "$token" ]; then
    if [ -n "$q_token" ]; then
      token=$(trim "$q_token")
    elif [ -n "$path_token" ]; then
      token=$(trim "$path_token")
    fi
  fi
}

valid_token() {
  local t=$1
  [ -n "$t" ] || return 1
  [ "${#t}" -le 200 ] || return 1
  case "$t" in
    *[!A-Za-z0-9._~+/=-]*) return 1 ;;
  esac
  return 0
}

skip_cli() {
  local v
  v=$(lower "$(trim "${RELAY_SKIP_CLI:-}")")
  case "$v" in
    1|true|yes|y) return 0 ;;
    *) return 1 ;;
  esac
}

site_root() {
  local origin
  origin=$(trim "${RELAY_SITE_ORIGIN:-https://jieshangai.com}")
  origin=${origin%/}
  # Plain http only for a gateway on this machine (local testing); anywhere
  # else the one-time ticket and the key would cross the network unencrypted.
  case "$origin" in
    https://*) ;;
    http://127.0.0.1|http://127.0.0.1:*|http://localhost|http://localhost:*|http://\[::1\]|http://\[::1\]:*) ;;
    *) return 1 ;;
  esac
  case "$origin" in
    *[[:space:]]*|*[\'\"\\@]*) return 1 ;;
  esac
  case "$origin" in
    */v1) origin=${origin%/v1} ;;
  esac
  printf '%s' "$origin"
}

have_cmd() { command -v "$1" >/dev/null 2>&1; }

refresh_user_path() {
  local root dir
  root=$(conf_root)
  for dir in "$root/.local/bin" "${HOME:-}/.local/bin" "/opt/homebrew/bin" "/usr/local/bin"; do
    [ -n "$dir" ] && [ -d "$dir" ] || continue
    case ":$PATH:" in
      *":$dir:"*) ;;
      *) PATH="$dir:$PATH" ;;
    esac
  done
  export PATH
  hash -r 2>/dev/null || true
}

# --- backup manifest (same format as client/core/backup.go) ---
# ~/.jieshangai/backup-manifest.tsv: header line, then sorted
# <relpath>\t<had_original 0|1>\t<sha256 of .bak|->\t<sha256 of our last write|->
manifest_path() { printf '%s' "$(conf_root)/.jieshangai/backup-manifest.tsv"; }

file_sha() {
  if have_cmd sha256sum; then
    sha256sum <"$1" | awk '{print $1}'
  elif have_cmd shasum; then
    shasum -a 256 <"$1" | awk '{print $1}'
  else
    python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
  fi
}

rel_of() {
  local root
  root=$(conf_root)
  root=${root%/}
  printf '%s' "${1#"$root"/}"
}

manifest_get() {
  local m
  m=$(manifest_path)
  [ -f "$m" ] || return 1
  awk -F '\t' -v k="$1" '$1 == k { print; found = 1; exit } END { exit !found }' "$m"
}

manifest_put() {
  local m dir tmp
  m=$(manifest_path)
  dir=$(dirname "$m")
  mkdir -p "$dir"
  tmp=$(mktemp "$dir/.manifest.XXXXXX")
  {
    printf '%s\n' "$MANIFEST_HEADER"
    {
      if [ -f "$m" ]; then
        awk -F '\t' -v k="$1" 'substr($0, 1, 1) != "#" && $0 != "" && $1 != k' "$m"
      fi
      printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4"
    } | LC_ALL=C sort
  } >"$tmp"
  chmod 600 "$tmp"
  mv -f "$tmp" "$m"
}

backup_once() {
  local f=$1 rel bak had=0 orig=-
  rel=$(rel_of "$f")
  manifest_get "$rel" >/dev/null && return 0
  bak="$f.jieshangai.bak"
  if [ -f "$bak" ]; then
    had=1
    orig=$(file_sha "$bak")
  elif [ -f "$f" ]; then
    cp -p "$f" "$bak"
    had=1
    orig=$(file_sha "$bak")
  fi
  manifest_put "$rel" "$had" "$orig" -
}

record_write() {
  local f=$1 rel line had orig
  rel=$(rel_of "$f")
  line=$(manifest_get "$rel") || return 0
  had=$(printf '%s' "$line" | awk -F '\t' '{print $2}')
  orig=$(printf '%s' "$line" | awk -F '\t' '{print $3}')
  manifest_put "$rel" "$had" "$orig" "$(file_sha "$f")"
}

shell_quote() {
  local s=$1
  s=${s//\'/\'\\\'\'}
  printf "'%s'" "$s"
}

write_claude_settings() {
  local path=$1 base=$2 secret=$3 root dir
  root=$(conf_root)
  dir=$(dirname "$path")
  mkdir -p "$dir"
  backup_once "$path"
  if have_cmd python3; then
    if ! RELAY_SETTINGS_PATH="$path" \
      RELAY_SETTINGS_BASE="$base" \
      RELAY_SETTINGS_SECRET="$secret" \
      RELAY_SETTINGS_CATALOG_STATUS="${CATALOG_STATUS:-}" \
      RELAY_SETTINGS_PRIMARY="${CATALOG_CLAUDE_PRIMARY:-}" \
      RELAY_SETTINGS_SONNET="${CATALOG_CLAUDE_SONNET:-}" \
      RELAY_SETTINGS_OPUS="${CATALOG_CLAUDE_OPUS:-}" \
      RELAY_SETTINGS_HAIKU="${CATALOG_CLAUDE_HAIKU:-}" \
      python3 - <<'PY'
import json, os, sys
path = os.environ["RELAY_SETTINGS_PATH"]
base = os.environ["RELAY_SETTINGS_BASE"]
secret = os.environ["RELAY_SETTINGS_SECRET"]
cat_status = os.environ.get("RELAY_SETTINGS_CATALOG_STATUS", "").strip()
m_primary = os.environ.get("RELAY_SETTINGS_PRIMARY", "").strip()
m_sonnet = os.environ.get("RELAY_SETTINGS_SONNET", "").strip()
m_opus = os.environ.get("RELAY_SETTINGS_OPUS", "").strip()
m_haiku = os.environ.get("RELAY_SETTINGS_HAIKU", "").strip()
try:
    data = {}
    if os.path.isfile(path) and os.path.getsize(path) > 0:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    if not isinstance(data, dict):
        sys.exit(2)
    env = data.get("env")
    if not isinstance(env, dict):
        env = {}
    env["ANTHROPIC_BASE_URL"] = base
    env["ANTHROPIC_AUTH_TOKEN"] = secret
    env.pop("ANTHROPIC_API_KEY", None)
    env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
    env["DISABLE_TELEMETRY"] = "1"
    env["DISABLE_ERROR_REPORTING"] = "1"
    if cat_status == "withdrawn":
        for k in ("ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"):
            env.pop(k, None)
    elif m_primary:
        env["ANTHROPIC_MODEL"] = m_primary
        if m_sonnet:
            env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = m_sonnet
        if m_opus:
            env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = m_opus
        if m_haiku:
            env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = m_haiku
    data["env"] = env
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=2)
        fh.write("\n")
    os.chmod(tmp, 0o600)
    os.replace(tmp, path)
except Exception:
    sys.exit(1)
PY
    then
      return 1
    fi
    record_write "$path"
    return
  fi
  if [ -e "$path" ]; then
    log_code 'config-write-failed'
    return 1
  fi
  local qbase qsecret
  qbase=$(json_quote "$base")
  qsecret=$(json_quote "$secret")
  local tmp
  tmp=$(mktemp "$dir/.settings.XXXXXX")
  cat >"$tmp" <<EOF
{
  "env": {
    "ANTHROPIC_BASE_URL": $qbase,
    "ANTHROPIC_AUTH_TOKEN": $qsecret,
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "DISABLE_TELEMETRY": "1",
    "DISABLE_ERROR_REPORTING": "1"
  }
}
EOF
  chmod 600 "$tmp"
  mv -f "$tmp" "$path"
  record_write "$path"
}

json_quote() {
  local s=$1
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  printf '"%s"' "$s"
}

insert_model_line() {
  printf '%s\n' "$1" | awk -v model="$2" -v tag="$MODEL_TAG" '
    { print }
    !done && /^model_provider[[:space:]]*=/ {
      print "model = \"" model "\" " tag
      done = 1
    }
    END {
      if (!done) print "model = \"" model "\" " tag
    }
  '
}

insert_catalog_line() {
  printf '%s\n' "$1" | awk -v catline="$2" '
    BEGIN { in_table = 0; replaced = 0 }
    /^\[/ { in_table = 1 }
    !in_table && !replaced && /^model_catalog_json[[:space:]]*=/ {
      lines[++n] = catline
      replaced = 1
      next
    }
    { lines[++n] = $0 }
    END {
      if (replaced) {
        for (i = 1; i <= n; i++) print lines[i]
        exit
      }
      insert_at = 0
      for (i = 1; i <= n; i++) {
        if (lines[i] ~ /^\[/) break
        if (lines[i] ~ /^(model|model_provider)[[:space:]]*=/) insert_at = i
      }
      if (insert_at == 0) {
        print catline
        for (i = 1; i <= n; i++) print lines[i]
      } else {
        for (i = 1; i <= n; i++) {
          print lines[i]
          if (i == insert_at) print catline
        }
      }
    }
  '
}

write_codex_config() {
  local path=$1 base=$2 model=$3 catalog_path=${4:-} dir text tmp
  dir=$(dirname "$path")
  mkdir -p "$dir"
  text=''
  backup_once "$path"
  if [ -f "$path" ]; then
    text=$(cat "$path")
  fi
  text=$(printf '%s\n' "$text" | awk -v start="$MARKER_START" -v end="$MARKER_END" '
    $0 == start { skip = 1; next }
    $0 == end { skip = 0; next }
    skip != 1 { print }
  ')
  if printf '%s\n' "$text" | grep -Eq '^model_provider[[:space:]]*='; then
    text=$(printf '%s\n' "$text" | sed -E 's/^model_provider[[:space:]]*=.*/model_provider = "jieshangai"/')
  else
    text="model_provider = \"jieshangai\"
$text"
  fi
  if [ -n "$model" ]; then
    if printf '%s\n' "$text" | grep -Eq '^model[[:space:]]*='; then
      text=$(printf '%s\n' "$text" | sed -E "s/^model[[:space:]]*=.*/model = \"${model}\" ${MODEL_TAG}/")
    else
      text=$(insert_model_line "$text" "$model")
    fi
  elif ! printf '%s\n' "$text" | grep -Eq '^model[[:space:]]*='; then
    text=$(insert_model_line "$text" "$DEFAULT_CODEX_MODEL")
  fi
  if [ -n "$catalog_path" ]; then
    local qcat
    qcat=$(json_quote "$catalog_path")
    text=$(insert_catalog_line "$text" "model_catalog_json = ${qcat} ${CATALOG_TAG}")
  elif [ "${CATALOG_STATUS:-}" = 'withdrawn' ]; then
    text=$(printf '%s\n' "$text" | sed -E '/^model_catalog_json[[:space:]]*=.*# jieshangai-catalog$/d')
  fi
  tmp=$(mktemp "$dir/.config.XXXXXX")
  {
    printf '%s\n' "$text"
    printf '\n%s\n' "$MARKER_START"
    printf '%s\n' "[model_providers.${PROVIDER_ID}]"
    printf '%s\n' 'name = "接上AI"'
    printf '%s\n' "base_url = \"${base}\""
    printf '%s\n' "env_key = \"${CODEX_ENV_NAME}\""
    printf '%s\n' 'wire_api = "responses"'
    printf '%s\n' "$MARKER_END"
  } >"$tmp"
  chmod 600 "$tmp"
  mv -f "$tmp" "$path"
  record_write "$path"
}

write_codex_env_file() {
  local path=$1 secret=$2 dir tmp quoted
  dir=$(dirname "$path")
  mkdir -p "$dir"
  quoted=$(shell_quote "$secret")
  tmp=$(mktemp "$dir/.env.XXXXXX")
  printf 'export %s=%s\n' "$CODEX_ENV_NAME" "$quoted" >"$tmp"
  chmod 600 "$tmp"
  mv -f "$tmp" "$path"
}

pick_rc() {
  local root shell_name
  root=$(conf_root)
  shell_name=$(basename "${SHELL:-bash}")
  case "$shell_name" in
    zsh) printf '%s\n' "$root/.zshrc" ;;
    *) printf '%s\n' "$root/.bashrc" ;;
  esac
}

write_shell_rc() {
  local rc=$1 text tmp dir
  dir=$(dirname "$rc")
  mkdir -p "$dir"
  text=''
  backup_once "$rc"
  if [ -f "$rc" ]; then
    text=$(cat "$rc")
    text=$(printf '%s\n' "$text" | awk -v start="$RC_START" -v end="$RC_END" '
      $0 == start { skip = 1; next }
      $0 == end { skip = 0; next }
      skip != 1 { print }
    ')
  fi
  tmp=$(mktemp "$dir/.rc.XXXXXX")
  {
    if [ -n "$text" ]; then
      printf '%s\n' "$text"
      printf '\n'
    fi
    printf '%s\n' "$RC_START"
    printf '%s\n' 'if [ -f "$HOME/.jieshangai/codex.env" ]; then'
    printf '%s\n' '  . "$HOME/.jieshangai/codex.env"'
    printf '%s\n' 'fi'
    printf '%s\n' "$RC_END"
  } >"$tmp"
  if [ -f "$rc" ]; then
    local mode
    mode=$(stat -c '%a' "$rc" 2>/dev/null || stat -f '%OLp' "$rc" 2>/dev/null || printf '644')
    chmod "$mode" "$tmp"
  else
    chmod 644 "$tmp"
  fi
  mv -f "$tmp" "$rc"
  record_write "$rc"
}

write_stamp() {
  local assistant=$1 cli=$2 root
  root=$(conf_root)
  mkdir -p "$root/.jieshangai"
  cat >"$root/.jieshangai/install-stamp.json" <<EOF
{"assistant":"$assistant","cli":"$cli","connection":"ok"}
EOF
  chmod 600 "$root/.jieshangai/install-stamp.json"
}

install_official() {
  local url=$1
  have_cmd curl || return 1
  curl -fsSL "$url" | bash
}

finish_claude() {
  local installed_now=$1 found=$2
  local os
  os=$(uname -s 2>/dev/null || printf 'unknown')
  say '好了。'
  case "$os" in
    Darwin|Linux)
      if [ "$installed_now" = '1' ]; then
        say '请新开一个命令窗口，输入 claude ，按回车。刚装上的话，这个窗口可能还找不到它。'
      else
        say '在命令窗口输入 claude ，按回车就能开始。'
      fi
      ;;
    *)
      if [ "$found" = '1' ]; then
        say '在命令窗口输入 claude ，按回车就能开始。'
      else
        say '如果这个窗口提示找不到 claude，关掉再开一次，然后输入 claude ，按回车。'
      fi
      ;;
  esac
}

finish_codex() {
  local found=$1
  local os
  os=$(uname -s 2>/dev/null || printf 'unknown')
  say '好了。'
  case "$os" in
    Darwin|Linux)
      say '请新开一个命令窗口，再输入 codex ，按回车。不重开会连不上。'
      ;;
    *)
      if [ "$found" = '1' ]; then
        say '在这个窗口输入 codex ，按回车。'
      else
        say '如果这个窗口提示找不到 codex，关掉再开一次，然后输入 codex ，按回车。'
      fi
      ;;
  esac
  say '如果它问要不要信任这个文件夹，选信任当前项目。'
}

main() {
  local agent='' token='' secret='' root origin cli_state found installed_now base rc
  agent=$(lower "$(trim "${RELAY_AGENT:-}")")
  token=$(trim "${RELAY_TOKEN:-}")
  if [ -n "${1:-}" ]; then
    fill_from_raw "$1"
  fi
  fill_from_raw "${RELAY_INSTALL_URL:-}"
  fill_from_raw "${RELAY_QUERY:-}"
  fill_from_raw "${QUERY_STRING:-}"

  if [ -z "$agent" ] && [ -z "$token" ]; then
    log_code 'missing-input'
    say '这行命令不完整，回到网页重新复制一次。'
    exit 1
  fi
  case "$agent" in
    claude|codex) ;;
    '')
      log_code 'bad-agent'
      say '不知道要装哪一个助手，回到网页重新复制一次命令。'
      exit 1
      ;;
    *)
      log_code 'bad-agent'
      say '不知道要装哪一个助手，回到网页重新复制一次命令。'
      exit 1
      ;;
  esac
  if ! valid_token "$token"; then
    log_code 'missing-input'
    say '这行命令不完整，回到网页重新复制一次。'
    exit 1
  fi

  secret=$(trim "${RELAY_SECRET:-}")
  if [ -z "$secret" ]; then
    secret=$token
  fi
  if ! valid_token "$secret" && [ "$secret" = "$token" ]; then
    log_code 'missing-input'
    say '这行命令不完整，回到网页重新复制一次。'
    exit 1
  fi
  case "$secret" in
    *[$'\n\r']*)
      log_code 'config-write-failed'
      say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
      exit 1
      ;;
  esac

  if ! origin=$(site_root); then
    log_code 'config-write-failed'
    say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
    exit 1
  fi
  REPORT_TOKEN=$token
  REPORT_ORIGIN=$origin
  report 1 running
  if claim_mode && [ -z "$(trim "${RELAY_SECRET:-}")" ]; then
    if ! secret=$(claim_secret "$origin" "$token"); then
      log_code 'claim-failed'
      say '连不上服务器，请检查网络后回到网页重新复制一次命令。'
      exit 1
    fi
  fi

  root=$(conf_root)
  report 2 running
  found=0
  installed_now=0
  cli_state='present'
  case "$agent" in
    claude) say '正在看看这台电脑上有没有 Claude Code……' ;;
    codex) say '正在看看这台电脑上有没有 Codex……' ;;
  esac
  refresh_user_path
  if have_cmd "$agent"; then
    found=1
    say '已经有了，不用再装。'
    cli_state='present'
  elif skip_cli; then
    say '这台电脑上还没有这个助手，这次没有去官网下载。'
    cli_state='skipped'
  else
    say '没找到，我去官网帮你装，可能要等一会儿。'
    local installer_status=1
    set +e
    case "$agent" in
      claude)
        install_official "$CLAUDE_INSTALL_URL"
        installer_status=$?
        ;;
      codex)
        CODEX_NON_INTERACTIVE=1 install_official "$CODEX_INSTALL_URL"
        installer_status=$?
        ;;
    esac
    set -e
    refresh_user_path
    if have_cmd "$agent"; then
      found=1
      installed_now=1
      cli_state='installed'
      say '装好了。'
    elif [ "$installer_status" -eq 0 ]; then
      installed_now=1
      cli_state='installed'
      say '装好了。'
    else
      cli_state='failed'
    fi
  fi

  report 3 running
  say '正在把连接写好……'
  fetch_install_catalog "$origin" "$secret" "$agent" "$root"
  if [ "$agent" = 'claude' ]; then
    if ! write_claude_settings "$root/.claude/settings.json" "$origin" "$secret"; then
      say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
      exit 1
    fi
  else
    local model=''
    model=$(trim "${RELAY_CODEX_MODEL:-}")
    case "$model" in
      ''|*[!A-Za-z0-9._:-]*)
        if [ -n "$model" ]; then
          log_code 'config-write-failed'
          say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
          exit 1
        fi
        ;;
    esac
    if [ -z "$model" ] && [ -n "${CATALOG_CODEX_MODEL:-}" ]; then
      model=$CATALOG_CODEX_MODEL
    fi
    if ! write_codex_config "$root/.codex/config.toml" "$origin/v1" "$model" "${CATALOG_CODEX_PATH:-}"; then
      log_code 'config-write-failed'
      say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
      exit 1
    fi
    if ! write_codex_env_file "$root/.jieshangai/codex.env" "$secret"; then
      log_code 'config-write-failed'
      say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
      exit 1
    fi
    rc=$(pick_rc)
    if ! write_shell_rc "$rc"; then
      log_code 'config-write-failed'
      say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
      exit 1
    fi
  fi

  if [ "$cli_state" = 'failed' ]; then
    log_code 'cli-install-failed'
    say '连接已经写好了，但助手没装上。回到网页看看下一步，或打开帮助页联系我们。'
    exit 1
  fi

  if claim_mode; then
    report 4 running
    say '正在测试连接……'
    if ! test_connection "$origin" "$secret"; then
      log_code 'connection-test-failed'
      say '连接没测通。检查网络后回到网页重新复制一次命令，或打开帮助页联系我们。'
      exit 1
    fi
  fi

  write_stamp "$agent" "$cli_state"
  clear_log
  report 5 'done'
  say '写好了。'
  if [ "$cli_state" = 'skipped' ]; then
    if [ "$agent" = 'claude' ]; then
      say '等助手装上之后，在命令窗口输入 claude ，按回车。'
    else
      say '等助手装上之后，请新开一个命令窗口，再输入 codex ，按回车。不重开会连不上。'
      say '如果它问要不要信任这个文件夹，选信任当前项目。'
    fi
    return 0
  fi
  if [ "$agent" = 'claude' ]; then
    finish_claude "$installed_now" "$found"
  else
    finish_codex "$found"
  fi
}

main "$@"
