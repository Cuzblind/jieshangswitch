#!/usr/bin/env bash
# 接上AI · 清掉本站写进这台电脑的连接（不卸载官方 CLI）
# 有备份且指纹对得上：换回装之前的原样；否则只去掉我方条目。
# 与 client/core（Restore）同一逻辑、同一份 backup-manifest.tsv。
# 写入后用户又改过：打印一行并要求确认（RELAY_CONFIRM=1 或在终端输入 y）。
#
# 正式形态：curl -fsSL https://jieshangai.com/u | bash
# 网关 /u 可注入 RELAY_SITE_ORIGIN。本脚本不调用撤销接口（本机通常没有登录态）。
# 设了 RELAY_HOME 时只动那个目录。屏幕上不打印连接内容。
set +x
if [ -z "${BASH_VERSION:-}" ]; then
  printf '%s\n' '请用 bash 打开命令窗口再粘贴一次。'
  exit 1
fi
set -euo pipefail
umask 077

MARKER_START='# jieshangai-provider-start'
MARKER_END='# jieshangai-provider-end'
RC_START='# jieshangai-connection-start'
RC_END='# jieshangai-connection-end'
NO_BACKUP_MSG='已去掉我们加的设置。如果你以前自己选过模型，需要重新选一次。'
RESTORED_MSG='已经恢复好了。'
EDITED_PROMPT='你后来自己改过的地方，也会一起恢复。要恢复请输入 y 后按回车：'
DECLINED_MSG='没有动任何文件。'

say() { printf '%s\n' "$*"; }
have_cmd() { command -v "$1" >/dev/null 2>&1; }

conf_root() {
  if [ -n "${RELAY_HOME:-}" ]; then
    printf '%s' "$RELAY_HOME"
  else
    printf '%s' "${HOME:?}"
  fi
}

strip_marked_file() {
  local path=$1 start=$2 end=$3 extra_sed=${4:-}
  [ -f "$path" ] || return 0
  # only rewrite files that carry our marks
  if ! grep -qxF -- "$start" "$path"; then
    if [ -z "$extra_sed" ] || ! grep -Eq '^model_provider[[:space:]]*=[[:space:]]*"jieshangai"[[:space:]]*$|^model[[:space:]]*=.*# jieshangai-model$' "$path"; then
      return 0
    fi
  fi
  local dir tmp text mode
  dir=$(dirname "$path")
  text=$(awk -v start="$start" -v end="$end" '
    $0 == start { skip = 1; next }
    $0 == end { skip = 0; next }
    skip != 1 { print }
  ' "$path")
  if [ -n "$extra_sed" ]; then
    text=$(printf '%s\n' "$text" | sed -E "$extra_sed")
  fi
  tmp=$(mktemp "$dir/.uninstall.XXXXXX")
  printf '%s\n' "$text" >"$tmp"
  mode=$(stat -c '%a' "$path" 2>/dev/null || stat -f '%OLp' "$path" 2>/dev/null || printf '644')
  chmod "$mode" "$tmp" 2>/dev/null || chmod 644 "$tmp"
  mv -f "$tmp" "$path"
}

clean_claude() {
  local path=$1
  [ -f "$path" ] || return 0
  if [ ! -s "$path" ]; then
    return 0
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    say '暂时没法改配置文件，请稍后再试。'
    exit 1
  fi
  if ! RELAY_SETTINGS_PATH="$path" python3 - <<'PY'
import json, os, sys
path = os.environ["RELAY_SETTINGS_PATH"]
keys = (
    "ANTHROPIC_BASE_URL",
    "ANTHROPIC_AUTH_TOKEN",
    "ANTHROPIC_API_KEY",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
    "DISABLE_TELEMETRY",
    "DISABLE_ERROR_REPORTING",
)
tmp = path + ".tmp"
try:
    with open(path, encoding="utf-8") as fh:
        raw = fh.read()
    if not raw.strip():
        sys.exit(0)
    data = json.loads(raw)
    if not isinstance(data, dict):
        sys.exit(0)
    env = data.get("env")
    if isinstance(env, dict):
        for key in keys:
            env.pop(key, None)
        if env:
            data["env"] = env
        else:
            data.pop("env", None)
    mode = os.stat(path).st_mode & 0o777
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=2)
        fh.write("\n")
    os.chmod(tmp, mode or 0o600)
    os.replace(tmp, path)
except Exception:
    try:
        os.remove(tmp)
    except OSError:
        pass
    sys.exit(1)
PY
  then
    say '配置文件读不了，请稍后再试。'
    exit 1
  fi
}

clean_jieshangai() {
  local root=$1 dir
  dir="$root/.jieshangai"
  rm -f "$dir/codex.env" "$dir/install-stamp.json" "$dir/install-error.log"
}

file_sha() {
  if have_cmd sha256sum; then
    sha256sum <"$1" | awk '{print $1}'
  elif have_cmd shasum; then
    shasum -a 256 <"$1" | awk '{print $1}'
  else
    python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
  fi
}

# manifest_lines <manifest>: entries sorted by path (header skipped)
manifest_lines() {
  [ -f "$1" ] || return 0
  awk 'substr($0, 1, 1) != "#" && $0 != ""' "$1" | LC_ALL=C sort
}

# edited_files <root> <manifest>: tracked files changed after our last write
edited_files() {
  local root=$1 m=$2 rel had orig written
  while IFS=$'\t' read -r rel had orig written; do
    [ -n "$rel" ] && [ "$written" != '-' ] || continue
    if [ ! -f "$root/$rel" ] || [ "$(file_sha "$root/$rel")" != "$written" ]; then
      printf '%s\n' "$root/$rel"
    fi
  done < <(manifest_lines "$m")
}

confirmed() {
  case "${RELAY_CONFIRM:-}" in 1|true|yes|y) return 0 ;; esac
  local ans=''
  printf '%s' "$EDITED_PROMPT"
  if ! { read -r ans </dev/tty; } 2>/dev/null; then
    printf '\n'
    return 1
  fi
  case "$ans" in y|Y|yes|YES) return 0 ;; esac
  return 1
}

remove_if_left_empty() {
  local f=$1 c
  [ -f "$f" ] || return 0
  c=$(cat "$f"; printf x)
  c=${c%x}
  case "$c" in
    ''|$'\n'|$'{}\n') rm -f "$f" ;;
  esac
}

main() {
  local root m used=0 rel had orig written path bak
  root=$(conf_root)
  root=${root%/}
  m="$root/.jieshangai/backup-manifest.tsv"
  if [ -n "$(edited_files "$root" "$m")" ]; then
    if ! confirmed; then
      say "$DECLINED_MSG"
      exit 1
    fi
  fi
  say '正在清掉本站写上的连接……'
  # settings.json: only when tracked, or no manifest (installs from before the manifest)
  if [ "$(manifest_lines "$m" | wc -l)" -eq 0 ] ||
    manifest_lines "$m" | awk -F '\t' '$1 == ".claude/settings.json" { f = 1 } END { exit !f }'; then
    clean_claude "$root/.claude/settings.json"
  fi
  strip_marked_file "$root/.codex/config.toml" "$MARKER_START" "$MARKER_END" \
    '/^model_provider[[:space:]]*=[[:space:]]*"jieshangai"[[:space:]]*$/d; /^model[[:space:]]*=.*# jieshangai-model$/d'
  strip_marked_file "$root/.zshrc" "$RC_START" "$RC_END"
  strip_marked_file "$root/.bashrc" "$RC_START" "$RC_END"
  clean_jieshangai "$root"
  while IFS=$'\t' read -r rel had orig written; do
    [ -n "$rel" ] || continue
    path="$root/$rel"
    bak="$path.jieshangai.bak"
    if [ "$had" = '1' ] && [ -f "$bak" ] && [ "$(file_sha "$bak")" = "$orig" ]; then
      cp -p "$bak" "$path"
      rm -f "$bak"
      used=1
    elif [ "$had" != '1' ]; then
      remove_if_left_empty "$path"
    fi
  done < <(manifest_lines "$m")
  rm -f "$m"
  rmdir "$root/.jieshangai" 2>/dev/null || true
  if [ "$used" = '1' ]; then
    say "$RESTORED_MSG"
  else
    say "$NO_BACKUP_MSG"
  fi
}

main "$@"
