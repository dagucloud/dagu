#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

RELEASES_URL="https://github.com/dagucloud/dagu/releases"
GITHUB_API_URL="https://api.github.com/repos/dagucloud/dagu/releases/latest"
FILE_BASENAME="dagu"
WINSW_VERSION="v2.12.0"
GUM_VERSION="0.17.0"

BOLD='\033[1m'
ACCENT='\033[38;2;11;93;68m'
INFO='\033[38;2;92;108;122m'
SUCCESS='\033[38;2;18;184;134m'
WARN='\033[38;2;194;124;14m'
ERROR='\033[38;2;196;62;62m'
MUTED='\033[38;2;120;128;142m'
NC='\033[0m'

TMPFILES=()
cleanup_tmpfiles() {
    local f
    for f in "${TMPFILES[@]:-}"; do
        rm -rf "$f" 2>/dev/null || true
    done
}
trap cleanup_tmpfiles EXIT

mktempdir() {
    local d
    if [[ -n "${WORKING_ROOT_DIR:-}" ]]; then
        mkdir -p "${WORKING_ROOT_DIR}" >/dev/null 2>&1 || true
        d="$(mktemp -d "${WORKING_ROOT_DIR%/}/dagu-installer.XXXXXX")"
    else
        d="$(mktemp -d)"
    fi
    TMPFILES+=("$d")
    printf '%s\n' "$d"
}

mktempfile() {
    local d f
    d="$(mktempdir)"
    f="${d}/tmpfile"
    : >"$f"
    printf '%s\n' "$f"
}

WORKING_ROOT_DIR=""
DAGU_INSTALL_DIR="${DAGU_INSTALL_DIR:-}"
VERSION=""
NO_PROMPT=0
DRY_RUN=0
VERBOSE=0
UNINSTALL_MODE=0
PURGE_DATA=0
REMOVE_SKILL=0
SERVICE_MODE=""
SERVICE_SCOPE=""
HOST=""
PORT=""
SKILL_MODE=""
declare -a EXPLICIT_SKILL_DIRS=()
ADMIN_USERNAME=""
ADMIN_PASSWORD=""
OPEN_BROWSER=""

GUM=""
GUM_STATUS="skipped"
GUM_REASON=""
DOWNLOADER=""
OS=""
ARCH=""
INSTALL_OWNER="${SUDO_USER:-$(id -un 2>/dev/null || printf '%s' "${USER:-unknown}")}"
INSTALL_GROUP="$(id -gn "${INSTALL_OWNER}" 2>/dev/null || printf '%s' "${INSTALL_OWNER}")"
SERVICE_ENV_FILE=""
SERVICE_BOOTSTRAP_FILE=""
SERVICE_UNIT_FILE=""
SERVICE_PLIST_FILE=""
SERVICE_LABEL="local.dagu.server"
DAGU_HOME_DIR=""
INSTALL_PATH=""
SERVICE_URL=""
SERVICE_PATH=""
SKILL_DETECTED_COUNT=0
SCRUB_BACKUP_FILE=""
declare -a UNINSTALL_INSTALL_PATHS=()
declare -a UNINSTALL_DAGU_HOMES=()
declare -a UNINSTALL_PATH_PROFILES=()
declare -a UNINSTALL_SKILL_DIRS=()
declare -a UNINSTALL_COPILOT_FILES=()
declare -a UNINSTALL_SERVICE_SCOPES=()
UNINSTALL_MAC_SERVICE=0
UNINSTALL_MULTIPLE_INSTALLS_CONFIRMED=0

usage() {
    cat <<'EOF'
Dagu installer wizard

Options:
  --uninstall                Remove Dagu, its background service, and installer PATH changes
  --purge-data               Also delete the detected Dagu data directory
  --remove-skill             Also remove Dagu AI skill installs
  --version <tag>            Install a specific version (for example: v1.24.0)
  --install-dir <path>       Install to a custom directory
  --prefix <path>            Alias for --install-dir
  --working-dir <path>       Store temporary files under this directory
  --service <yes|no>         Install and start Dagu as a background service
  --service-scope <scope>    Service scope: user or system
  --host <host>              Host address for the Dagu web UI
  --port <port>              Port for the Dagu web UI
  --skills-dir <path>        Deprecated; use gh skill install dagucloud/dagu dagu instead
  --admin-username <name>    Initial admin username for builtin auth bootstrap
  --admin-password <pass>    Initial admin password for builtin auth bootstrap
  --open-browser <yes|no>    Open the Dagu URL after successful setup
  --no-prompt                Disable interactive prompts
  --dry-run                  Print the plan without making changes
  --verbose                  Show command output during install
  --help                     Show this help message
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            shift
            VERSION="${1:-}"
            ;;
        --install-dir|--prefix)
            shift
            DAGU_INSTALL_DIR="${1:-}"
            ;;
        --working-dir)
            shift
            WORKING_ROOT_DIR="${1:-}"
            ;;
        --service)
            shift
            SERVICE_MODE="${1:-}"
            ;;
        --service-scope)
            shift
            SERVICE_SCOPE="${1:-}"
            ;;
        --host)
            shift
            HOST="${1:-}"
            ;;
        --port)
            shift
            PORT="${1:-}"
            ;;
        --skills-dir)
            shift
            EXPLICIT_SKILL_DIRS+=("${1:-}")
            ;;
        --admin-username)
            shift
            ADMIN_USERNAME="${1:-}"
            ;;
        --admin-password)
            shift
            ADMIN_PASSWORD="${1:-}"
            ;;
        --open-browser)
            shift
            OPEN_BROWSER="${1:-}"
            ;;
        --no-prompt)
            NO_PROMPT=1
            ;;
        --dry-run)
            DRY_RUN=1
            ;;
        --verbose)
            VERBOSE=1
            ;;
        --uninstall)
            UNINSTALL_MODE=1
            ;;
        --purge-data)
            PURGE_DATA=1
            ;;
        --remove-skill)
            REMOVE_SKILL=1
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            printf '%s\n' "Unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

detect_downloader() {
    if command -v curl >/dev/null 2>&1; then
        DOWNLOADER="curl"
        return 0
    fi
    if command -v wget >/dev/null 2>&1; then
        DOWNLOADER="wget"
        return 0
    fi
    printf '%s\n' "Missing downloader: install curl or wget." >&2
    exit 1
}

download_file() {
    local url="$1"
    local output="$2"
    detect_downloader
    if [[ "$DOWNLOADER" == "curl" ]]; then
        curl -fsSL --proto '=https' --tlsv1.2 --retry 3 --retry-delay 1 --retry-connrefused -o "$output" "$url"
    else
        wget -q --https-only --secure-protocol=TLSv1_2 --tries=3 --timeout=20 -O "$output" "$url"
    fi
}

download_stdout() {
    local url="$1"
    detect_downloader
    if [[ "$DOWNLOADER" == "curl" ]]; then
        curl -fsSL --proto '=https' --tlsv1.2 --retry 3 --retry-delay 1 --retry-connrefused "$url"
    else
        wget -qO- --https-only --secure-protocol=TLSv1_2 --tries=3 --timeout=20 "$url"
    fi
}

is_non_interactive_shell() {
    if [[ "$NO_PROMPT" == "1" ]]; then
        return 0
    fi
    if [[ ! -t 0 || ! -t 1 ]]; then
        return 0
    fi
    return 1
}

gum_is_tty() {
    if [[ -n "${NO_COLOR:-}" ]]; then
        return 1
    fi
    if [[ "${TERM:-dumb}" == "dumb" ]]; then
        return 1
    fi
    if [[ -t 2 || -t 1 ]]; then
        return 0
    fi
    if [[ -r /dev/tty && -w /dev/tty ]]; then
        return 0
    fi
    return 1
}

verify_sha256sum_file() {
    local checksums="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        # NOTE: Callers must pre-filter $checksums to contain only the entry
        # for the file being verified. This avoids --ignore-missing, which is
        # not supported by all sha256sum implementations (e.g., BusyBox).
        sha256sum -c "$checksums" >/dev/null 2>&1
        return $?
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -c "$checksums" >/dev/null 2>&1
        return $?
    fi
    return 1
}

quote_env_value() {
    local value="$1"
    value=${value//\'/\'\\\'\'}
    printf "'%s'" "$value"
}

xml_escape() {
    local value="$1"
    value=${value//&/&amp;}
    value=${value//</&lt;}
    value=${value//>/&gt;}
    value=${value//\"/&quot;}
    value=${value//\'/&apos;}
    printf '%s' "$value"
}

json_escape() {
    local value="$1"
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    value=${value//$'\t'/\\t}
    printf '%s' "$value"
}

has_admin_bootstrap() {
    [[ -n "${ADMIN_USERNAME}" && -n "${ADMIN_PASSWORD}" ]]
}

validate_admin_bootstrap() {
    if [[ -n "${ADMIN_USERNAME}" && -z "${ADMIN_PASSWORD}" ]]; then
        ui_error "An admin password is required when --admin-username is provided."
        exit 2
    fi
    if [[ -z "${ADMIN_USERNAME}" && -n "${ADMIN_PASSWORD}" ]]; then
        ui_error "An admin username is required when --admin-password is provided."
        exit 2
    fi
    if has_admin_bootstrap && [[ "${#ADMIN_PASSWORD}" -lt 8 ]]; then
        ui_error "The admin password must be at least 8 characters."
        exit 1
    fi
}

bootstrap_gum_temp() {
    local os_name gum_arch asset base gum_tmp asset_sum
    GUM=""
    GUM_STATUS="skipped"
    GUM_REASON=""

    if is_non_interactive_shell; then
        GUM_REASON="non-interactive shell"
        return 1
    fi
    if ! gum_is_tty; then
        GUM_REASON="terminal does not support gum"
        return 1
    fi
    if command -v gum >/dev/null 2>&1; then
        GUM="gum"
        GUM_STATUS="found"
        GUM_REASON="already installed"
        return 0
    fi
    if ! command -v tar >/dev/null 2>&1; then
        GUM_REASON="tar not available"
        return 1
    fi

    case "$(uname -s 2>/dev/null || true)" in
        Darwin) os_name="Darwin" ;;
        Linux) os_name="Linux" ;;
        *) GUM_REASON="unsupported OS"; return 1 ;;
    esac
    case "$(uname -m 2>/dev/null || true)" in
        x86_64|amd64) gum_arch="x86_64" ;;
        arm64|aarch64) gum_arch="arm64" ;;
        i386|i686) gum_arch="i386" ;;
        armv7l|armv7) gum_arch="armv7" ;;
        armv6l|armv6) gum_arch="armv6" ;;
        *) GUM_REASON="unsupported architecture"; return 1 ;;
    esac

    gum_tmp="$(mktempdir)"
    asset="gum_${GUM_VERSION}_${os_name}_${gum_arch}.tar.gz"
    base="https://github.com/charmbracelet/gum/releases/download/v${GUM_VERSION}"

    if ! download_file "${base}/${asset}" "${gum_tmp}/${asset}"; then
        GUM_REASON="download failed"
        return 1
    fi
    if ! download_file "${base}/checksums.txt" "${gum_tmp}/checksums.txt"; then
        GUM_REASON="checksum download failed"
        return 1
    fi

    asset_sum="${gum_tmp}/checksums-${asset}.txt"
    grep " ${asset}\$" "${gum_tmp}/checksums.txt" >"${asset_sum}" 2>/dev/null || true
    if [[ ! -s "${asset_sum}" ]]; then
        GUM_REASON="checksum entry missing"
        return 1
    fi
    if ! (cd "${gum_tmp}" && verify_sha256sum_file "$(basename "${asset_sum}")"); then
        GUM_REASON="checksum verification failed"
        return 1
    fi
    if ! tar -xzf "${gum_tmp}/${asset}" -C "${gum_tmp}" >/dev/null 2>&1; then
        GUM_REASON="extract failed"
        return 1
    fi
    GUM="$(find "${gum_tmp}" -type f -name gum 2>/dev/null | head -n1 || true)"
    if [[ -z "${GUM}" || ! -f "${GUM}" ]]; then
        GUM_REASON="gum binary missing"
        return 1
    fi
    chmod +x "${GUM}" >/dev/null 2>&1 || true
    GUM_STATUS="installed"
    GUM_REASON="temporary verified install"
    return 0
}

ui_info() {
    local msg="$*"
    if [[ -n "$GUM" ]]; then
        "$GUM" log --level info "$msg"
    else
        printf '%b·%b %s\n' "${MUTED}" "${NC}" "$msg"
    fi
}

ui_warn() {
    local msg="$*"
    if [[ -n "$GUM" ]]; then
        "$GUM" log --level warn "$msg"
    else
        printf '%b!%b %s\n' "${WARN}" "${NC}" "$msg"
    fi
}

ui_success() {
    local msg="$*"
    if [[ -n "$GUM" ]]; then
        local mark
        mark="$("$GUM" style --foreground "#12b886" --bold "✓")"
        printf '%s %s\n' "$mark" "$msg"
    else
        printf '%b✓%b %s\n' "${SUCCESS}" "${NC}" "$msg"
    fi
}

ui_error() {
    local msg="$*"
    if [[ -n "$GUM" ]]; then
        "$GUM" log --level error "$msg"
    else
        printf '%b✗%b %s\n' "${ERROR}" "${NC}" "$msg"
    fi
}

ui_section() {
    local title="$1"
    if [[ -n "$GUM" ]]; then
        "$GUM" style --bold --foreground "#0b5d44" --padding "1 0" "$title"
    else
        printf '\n%b%b%s%b\n' "${ACCENT}" "${BOLD}" "$title" "${NC}"
    fi
}

ui_kv() {
    local key="$1"
    local value="$2"
    if [[ -n "$GUM" ]]; then
        local key_part value_part
        key_part="$("$GUM" style --foreground "#78808e" --width 20 "$key")"
        value_part="$("$GUM" style --bold "$value")"
        "$GUM" join --horizontal "$key_part" "$value_part"
    else
        printf '%b%s:%b %s\n' "${MUTED}" "$key" "${NC}" "$value"
    fi
}

print_banner() {
    if [[ -n "$GUM" ]]; then
        local title tagline card
        title="$("$GUM" style --foreground "#0b5d44" --bold "Dagu Installer")"
        tagline="$("$GUM" style --foreground "#5c6c7a" "Install Dagu, set it up as a background app, and get you to the UI quickly.")"
        card="$(printf '%s\n%s' "$title" "$tagline")"
        "$GUM" style --border rounded --border-foreground "#0b5d44" --padding "1 2" "$card"
        printf '\n'
    else
        printf '%b%bDagu Installer%b\n' "${ACCENT}" "${BOLD}" "${NC}"
        printf '%bInstall Dagu, set it up as a background app, and get you to the UI quickly.%b\n\n' "${INFO}" "${NC}"
    fi
}

run_with_spinner() {
    local title="$1"
    shift
    if [[ -n "$GUM" && "$VERBOSE" != "1" ]]; then
        "$GUM" spin --spinner dot --title "$title" -- "$@"
    else
        "$@"
    fi
}

run_quiet_step() {
    local title="$1"
    shift
    local log
    if [[ "$VERBOSE" == "1" ]]; then
        run_with_spinner "$title" "$@"
        return $?
    fi
    log="$(mktempfile)"
    if run_with_spinner "$title" bash -c "$(printf '%q ' "$@") >$(printf '%q' "$log") 2>&1"; then
        return 0
    fi
    ui_error "${title} failed"
    if [[ -s "$log" ]]; then
        tail -n 80 "$log" >&2 || true
    fi
    return 1
}

is_promptable() {
    if [[ "$NO_PROMPT" == "1" ]]; then
        return 1
    fi
    if [[ -r /dev/tty && -w /dev/tty ]]; then
        return 0
    fi
    return 1
}

prompt_line() {
    local prompt="$1"
    local answer=""
    if ! is_promptable; then
        return 1
    fi
    printf '%s' "$prompt" > /dev/tty
    IFS= read -r answer < /dev/tty || true
    printf '%s' "$answer"
}

prompt_yes_no() {
    local prompt="$1"
    local default_answer="$2"
    local answer=""
    if ! is_promptable; then
        [[ "$default_answer" == "yes" ]]
        return $?
    fi
    if [[ "$default_answer" == "yes" ]]; then
        answer="$(prompt_line "${prompt} [Y/n]: " || true)"
        case "${answer:-}" in
            n|N|no|NO) return 1 ;;
            *) return 0 ;;
        esac
    else
        answer="$(prompt_line "${prompt} [y/N]: " || true)"
        case "${answer:-}" in
            y|Y|yes|YES) return 0 ;;
            *) return 1 ;;
        esac
    fi
}

prompt_text() {
    local label="$1"
    local default_value="$2"
    local answer=""
    if ! is_promptable; then
        printf '%s' "$default_value"
        return 0
    fi
    if [[ -n "$default_value" ]]; then
        answer="$(prompt_line "${label} [${default_value}]: " || true)"
        printf '%s' "${answer:-$default_value}"
    else
        answer="$(prompt_line "${label}: " || true)"
        printf '%s' "$answer"
    fi
}

prompt_secret_confirm() {
    local label="$1"
    local first="" second=""
    if ! is_promptable; then
        return 1
    fi
    while true; do
        printf '%s: ' "$label" > /dev/tty
        IFS= read -r -s first < /dev/tty || true
        printf '\nConfirm %s: ' "$label" > /dev/tty
        IFS= read -r -s second < /dev/tty || true
        printf '\n' > /dev/tty
        if [[ "$first" != "$second" ]]; then
            ui_warn "Passwords did not match. Try again."
            continue
        fi
        printf '%s' "$first"
        return 0
    done
}

resolve_version() {
    if [[ -n "$VERSION" ]]; then
        if [[ "${VERSION}" == "latest" ]]; then
            VERSION=""
        elif [[ "${VERSION}" != v* ]]; then
            VERSION="v${VERSION}"
            return 0
        else
            return 0
        fi
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        VERSION="latest"
        return 0
    fi
    local body
    body="$(download_stdout "$GITHUB_API_URL" 2>/dev/null || true)"
    VERSION="$(printf '%s' "$body" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed 's/.*"\([^"]*\)".*/\1/')"
    if [[ -n "$VERSION" ]]; then
        return 0
    fi
    VERSION="$(download_stdout "${RELEASES_URL}/latest" 2>/dev/null | sed -n 's#.*/tag/\([^"]*\).*#\1#p' | head -n1 || true)"
    if [[ -z "$VERSION" ]]; then
        ui_error "Failed to determine the latest Dagu version."
        exit 1
    fi
}

detect_os_arch() {
    case "$(uname -s 2>/dev/null || true)" in
        Darwin) OS="macos"; ARCHIVE_OS="darwin" ;;
        Linux) OS="linux"; ARCHIVE_OS="linux" ;;
        *) ui_error "Unsupported operating system. Use the Windows PowerShell installer on Windows."; exit 1 ;;
    esac
    case "$(uname -m 2>/dev/null || true)" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        armv7l|armv7) ARCH="armv7" ;;
        armv6l|armv6) ARCH="armv6" ;;
        i386|i686) ARCH="386" ;;
        ppc64le) ARCH="ppc64le" ;;
        s390x) ARCH="s390x" ;;
        *) ui_error "Unsupported architecture: $(uname -m)"; exit 1 ;;
    esac
}

detect_skill_targets() {
    local home_dir codex_dir
    home_dir="${HOME}"
    SKILL_DETECTED_COUNT=0
    if [[ -f "${home_dir}/.claude/.claude.json" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    fi
    if [[ -d "${AGENTS_HOME:-${home_dir}/.agents}" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    elif [[ -d "${CODEX_HOME:-${home_dir}/.codex}" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    fi
    if [[ -d "${home_dir}/.config/opencode" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    fi
    if [[ -f "${home_dir}/.gemini/GEMINI.md" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    fi
    if [[ -f "${XDG_CONFIG_HOME:-${home_dir}}/.copilot/config.json" || -f "${home_dir}/.copilot/config.json" ]]; then
        SKILL_DETECTED_COUNT=$((SKILL_DETECTED_COUNT + 1))
    fi
}

default_install_dir() {
    if [[ -n "$DAGU_INSTALL_DIR" ]]; then
        printf '%s' "$DAGU_INSTALL_DIR"
        return 0
    fi
    if [[ "$SERVICE_MODE" == "yes" && "$OS" == "linux" && "$SERVICE_SCOPE" == "system" ]]; then
        printf '/usr/local/bin'
    else
        printf '%s/.local/bin' "$HOME"
    fi
}

default_dagu_home() {
    if [[ "$OS" == "linux" && "$SERVICE_MODE" == "yes" && "$SERVICE_SCOPE" == "system" ]]; then
        printf '/var/lib/dagu'
    else
        printf '%s/.dagu' "$HOME"
    fi
}

configure_defaults() {
    if [[ -z "$SERVICE_MODE" ]]; then
        if is_promptable; then
            SERVICE_MODE="yes"
        else
            SERVICE_MODE="no"
        fi
    fi
    if [[ "$SERVICE_MODE" != "yes" && "$SERVICE_MODE" != "no" ]]; then
        ui_error "Invalid --service value: $SERVICE_MODE"
        exit 2
    fi
    if [[ "$SERVICE_MODE" == "yes" && -z "$SERVICE_SCOPE" ]]; then
        if [[ "$OS" == "linux" ]]; then
            if command -v sudo >/dev/null 2>&1; then
                SERVICE_SCOPE="system"
            else
                SERVICE_SCOPE="user"
            fi
        else
            SERVICE_SCOPE="user"
        fi
    fi
    if [[ "$OS" != "linux" ]]; then
        SERVICE_SCOPE="user"
    fi
    if [[ "$SERVICE_MODE" == "no" ]]; then
        SERVICE_SCOPE="user"
    fi
    if [[ "$SERVICE_SCOPE" != "user" && "$SERVICE_SCOPE" != "system" ]]; then
        ui_error "Invalid --service-scope value: $SERVICE_SCOPE"
        exit 2
    fi
    if [[ -z "$HOST" ]]; then
        HOST="127.0.0.1"
    fi
    if [[ -z "$PORT" ]]; then
        PORT="8080"
    fi
    DAGU_INSTALL_DIR="$(default_install_dir)"
    DAGU_HOME_DIR="$(default_dagu_home)"
    INSTALL_PATH="${DAGU_INSTALL_DIR}/dagu"
    SERVICE_URL="http://${HOST}:${PORT}"
    resolve_service_path
    if [[ -z "$OPEN_BROWSER" ]]; then
        OPEN_BROWSER="yes"
    fi
    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -gt 0 ]]; then
        SKILL_MODE="explicit"
    elif [[ -z "${SKILL_MODE}" ]]; then
        if [[ "$SKILL_DETECTED_COUNT" -gt 0 ]]; then
            SKILL_MODE="auto"
        else
            SKILL_MODE="skip"
        fi
    fi
}

append_service_path_segment() {
    local segment="$1"
    [[ -z "$segment" ]] && return 0
    case ":${SERVICE_PATH}:" in
        *":${segment}:"*) return 0 ;;
    esac
    if [[ -z "${SERVICE_PATH}" ]]; then
        SERVICE_PATH="${segment}"
    else
        SERVICE_PATH="${SERVICE_PATH}:${segment}"
    fi
}

append_service_path_list() {
    local list="$1"
    local segment
    local old_ifs="${IFS}"
    [[ -z "$list" ]] && return 0
    IFS=':'
    for segment in $list; do
        append_service_path_segment "$segment"
    done
    IFS="${old_ifs}"
}

resolve_service_path() {
    local path_helper_output=""
    SERVICE_PATH=""
    append_service_path_list "${PATH:-}"
    if [[ "$OS" == "macos" && -x /usr/libexec/path_helper ]]; then
        path_helper_output="$("/usr/libexec/path_helper" -s 2>/dev/null | sed -n 's/^PATH=\"\(.*\)\"; export PATH$/\1/p' | head -n1 || true)"
        append_service_path_list "${path_helper_output}"
    fi
    append_service_path_segment "${DAGU_INSTALL_DIR}"
    append_service_path_segment "${HOME}/.local/bin"
    append_service_path_segment "${HOME}/bin"
    append_service_path_segment "${HOME}/.npm-global/bin"
    append_service_path_segment "${HOME}/.local/share/pnpm"
    append_service_path_segment "${HOME}/.bun/bin"
    append_service_path_segment "/opt/homebrew/bin"
    append_service_path_segment "/usr/local/bin"
    append_service_path_segment "/usr/bin"
    append_service_path_segment "/bin"
    append_service_path_segment "/usr/sbin"
    append_service_path_segment "/sbin"
    if [[ -z "${SERVICE_PATH}" ]]; then
        SERVICE_PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
    fi
}

add_uninstall_install_path() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_INSTALL_PATHS[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_INSTALL_PATHS+=("$item")
}

add_uninstall_dagu_home() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_DAGU_HOMES[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_DAGU_HOMES+=("$item")
}

add_uninstall_path_profile() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_PATH_PROFILES[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_PATH_PROFILES+=("$item")
}

add_uninstall_skill_dir() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_SKILL_DIRS[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_SKILL_DIRS+=("$item")
}

add_uninstall_copilot_file() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_COPILOT_FILES[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_COPILOT_FILES+=("$item")
}

add_uninstall_service_scope() {
    local item="$1"
    local existing
    [[ -z "$item" ]] && return 0
    for existing in "${UNINSTALL_SERVICE_SCOPES[@]:-}"; do
        [[ "$existing" == "$item" ]] && return 0
    done
    UNINSTALL_SERVICE_SCOPES+=("$item")
}

join_with_comma() {
    local first=1
    local value
    for value in "$@"; do
        [[ -z "$value" ]] && continue
        if [[ "$first" == "1" ]]; then
            printf '%s' "$value"
            first=0
        else
            printf ', %s' "$value"
        fi
    done
}

strip_matching_install_paths() {
    local target="$1"
    local filtered=()
    local path
    for path in "${UNINSTALL_INSTALL_PATHS[@]:-}"; do
        if [[ "$path" == "$target" ]]; then
            filtered+=("$path")
        fi
    done
    UNINSTALL_INSTALL_PATHS=("${filtered[@]}")
}

strip_matching_service_scopes() {
    local target="$1"
    local filtered=()
    local scope
    for scope in "${UNINSTALL_SERVICE_SCOPES[@]:-}"; do
        if [[ "$scope" == "$target" ]]; then
            filtered+=("$scope")
        fi
    done
    UNINSTALL_SERVICE_SCOPES=("${filtered[@]}")
}

choose_operation_mode() {
    if [[ "$UNINSTALL_MODE" == "1" ]]; then
        return 0
    fi
    if ! is_promptable; then
        return 0
    fi
    ui_section "Choose setup"
    if prompt_yes_no "Install or repair Dagu now?" "yes"; then
        UNINSTALL_MODE=0
    else
        UNINSTALL_MODE=1
    fi
}

validate_uninstall_flags() {
    if [[ "$UNINSTALL_MODE" != "1" ]]; then
        return 0
    fi
    if [[ -n "$VERSION" ]]; then
        ui_error "--version is only supported during install."
        exit 2
    fi
    if [[ -n "$HOST" || -n "$PORT" ]]; then
        ui_error "--host and --port are only supported during install."
        exit 2
    fi
    if [[ -n "$ADMIN_USERNAME" || -n "$ADMIN_PASSWORD" ]]; then
        ui_error "Admin bootstrap flags are only supported during install."
        exit 2
    fi
    if [[ -n "$OPEN_BROWSER" ]]; then
        ui_error "--open-browser is only supported during install."
        exit 2
    fi
    if [[ -n "$SERVICE_MODE" ]]; then
        ui_error "--service is only supported during install. Use --service-scope to target a specific uninstall scope."
        exit 2
    fi
    if [[ -n "$SERVICE_SCOPE" && "$SERVICE_SCOPE" != "user" && "$SERVICE_SCOPE" != "system" ]]; then
        ui_error "Invalid --service-scope value: $SERVICE_SCOPE"
        exit 2
    fi
    if [[ "$OS" == "macos" && -n "$SERVICE_SCOPE" && "$SERVICE_SCOPE" != "user" ]]; then
        ui_error "macOS uninstall only supports --service-scope user."
        exit 2
    fi
}

run_install_wizard() {
    local answer
    if ! is_promptable; then
        return 0
    fi
    ui_section "Recommended setup"
    ui_info "This wizard can install Dagu, run it in the background, create your first admin, and install the Dagu AI skill."

    if prompt_yes_no "Install Dagu as a background service?" "yes"; then
        SERVICE_MODE="yes"
    else
        SERVICE_MODE="no"
        SERVICE_SCOPE="user"
    fi

    if [[ "$SERVICE_MODE" == "yes" && "$OS" == "linux" ]]; then
        if prompt_yes_no "Use a system-wide Linux service?" "yes"; then
            SERVICE_SCOPE="system"
        else
            SERVICE_SCOPE="user"
        fi
    fi

    if prompt_yes_no "Open Dagu only on this computer?" "yes"; then
        HOST="127.0.0.1"
    else
        HOST="0.0.0.0"
    fi

    PORT="$(prompt_text "Web UI port" "${PORT}")"
    DAGU_INSTALL_DIR="$(prompt_text "Install directory" "${DAGU_INSTALL_DIR}")"
    INSTALL_PATH="${DAGU_INSTALL_DIR}/dagu"

    if [[ "$SERVICE_MODE" == "yes" ]]; then
        DAGU_HOME_DIR="$(prompt_text "Dagu data directory" "${DAGU_HOME_DIR}")"
        ADMIN_USERNAME="$(prompt_text "Initial admin username" "${ADMIN_USERNAME:-admin}")"
        if [[ -z "$ADMIN_PASSWORD" ]]; then
            ADMIN_PASSWORD="$(prompt_secret_confirm "Initial admin password")"
        fi
        if [[ "${#ADMIN_PASSWORD}" -lt 8 ]]; then
            ui_error "The admin password must be at least 8 characters."
            exit 1
        fi
    fi

    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -eq 0 ]]; then
        if [[ "$SKILL_DETECTED_COUNT" -gt 0 ]]; then
            if prompt_yes_no "Install the Dagu AI skill into detected AI tools?" "yes"; then
                SKILL_MODE="auto"
            else
                SKILL_MODE="skip"
            fi
        elif prompt_yes_no "Install the Dagu AI skill into a custom skills directory?" "no"; then
            answer="$(prompt_text "Skills directory" "${HOME}/.agents/skills")"
            if [[ -n "$answer" ]]; then
                EXPLICIT_SKILL_DIRS+=("$answer")
                SKILL_MODE="explicit"
            fi
        else
            SKILL_MODE="skip"
        fi
    fi
}

show_install_plan() {
    ui_section "Install plan"
    ui_kv "Version" "${VERSION}"
    ui_kv "OS" "${OS}"
    ui_kv "Architecture" "${ARCH}"
    ui_kv "Install directory" "${DAGU_INSTALL_DIR}"
    ui_kv "Run as background app" "${SERVICE_MODE}"
    if [[ "$SERVICE_MODE" == "yes" ]]; then
        ui_kv "Service scope" "${SERVICE_SCOPE}"
        ui_kv "Dagu home" "${DAGU_HOME_DIR}"
        ui_kv "URL" "${SERVICE_URL}"
        ui_kv "Admin bootstrap" "${ADMIN_USERNAME:-disabled}"
    fi
    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -gt 0 ]]; then
        ui_kv "Skill install" "custom"
    elif [[ "${SKILL_MODE:-skip}" == "auto" ]]; then
        ui_kv "Skill install" "detected AI tools"
    else
        ui_kv "Skill install" "skip"
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_kv "Dry run" "yes"
    fi
}

read_file_maybe_sudo() {
    local file="$1"
    if [[ -r "$file" ]]; then
        cat "$file"
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        if is_promptable; then
            sudo cat "$file" 2>/dev/null || true
        else
            sudo -n cat "$file" 2>/dev/null || true
        fi
    fi
}

read_env_value_maybe_sudo() {
    local file="$1"
    local key="$2"
    if [[ -r "$file" ]]; then
        read_env_value "$file" "$key"
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        if is_promptable; then
            sudo env KEY_NAME="$key" sh -c '
                . "$1" >/dev/null 2>&1 || exit 1
                eval "printf %s \"\${$KEY_NAME}\""
            ' sh "$file" 2>/dev/null || true
        else
            sudo -n env KEY_NAME="$key" sh -c '
                . "$1" >/dev/null 2>&1 || exit 1
                eval "printf %s \"\${$KEY_NAME}\""
            ' sh "$file" 2>/dev/null || true
        fi
    fi
}

xml_unescape() {
    local value="$1"
    value=${value//&apos;/\'}
    value=${value//&quot;/\"}
    value=${value//&gt;/>}
    value=${value//&lt;/<}
    value=${value//&amp;/&}
    printf '%s' "$value"
}

read_systemd_setting() {
    local file="$1"
    local key="$2"
    read_file_maybe_sudo "$file" | sed -n "s/^${key}=//p" | head -n1
}

read_mac_plist_string_key() {
    local file="$1"
    local key="$2"
    local value
    value="$(read_file_maybe_sudo "$file" | awk -v wanted="$key" '
        $0 ~ "<key>" wanted "</key>" {
            getline
            if ($0 ~ /<string>/) {
                sub(/.*<string>/, "", $0)
                sub(/<\/string>.*/, "", $0)
                print
                exit
            }
        }
    ')"
    xml_unescape "$value"
}

read_mac_program_argument0() {
    local file="$1"
    local value
    value="$(read_file_maybe_sudo "$file" | awk '
        /<key>ProgramArguments<\/key>/ { in_args=1; next }
        in_args && /<string>/ {
            sub(/.*<string>/, "", $0)
            sub(/<\/string>.*/, "", $0)
            print
            exit
        }
    ')"
    xml_unescape "$value"
}

find_managed_path_profiles() {
    local profile
    for profile in "${HOME}/.zprofile" "${HOME}/.bash_profile" "${HOME}/.bashrc" "${HOME}/.profile"; do
        if [[ -f "$profile" ]] && grep -Fq "# >>> dagu installer >>>" "$profile" 2>/dev/null; then
            add_uninstall_path_profile "$profile"
        fi
    done
}

discover_linux_uninstall_scope() {
    local scope="$1"
    local discovered_exec="" discovered_home=""
    SERVICE_SCOPE="$scope"
    linux_service_files
    if [[ ! -f "${SERVICE_UNIT_FILE}" ]]; then
        return 0
    fi
    add_uninstall_service_scope "$scope"
    discovered_exec="$(read_systemd_setting "${SERVICE_UNIT_FILE}" "ExecStart")"
    discovered_exec="${discovered_exec%% *}"
    add_uninstall_install_path "$discovered_exec"
    discovered_home="$(read_env_value_maybe_sudo "${SERVICE_ENV_FILE}" "DAGU_HOME")"
    if [[ -z "$discovered_home" ]]; then
        discovered_home="$(read_systemd_setting "${SERVICE_UNIT_FILE}" "WorkingDirectory")"
    fi
    add_uninstall_dagu_home "$discovered_home"
}

discover_mac_uninstall() {
    local discovered_exec="" discovered_home=""
    mac_service_files
    if [[ ! -f "${SERVICE_PLIST_FILE}" ]]; then
        return 0
    fi
    UNINSTALL_MAC_SERVICE=1
    discovered_exec="$(read_mac_program_argument0 "${SERVICE_PLIST_FILE}")"
    add_uninstall_install_path "$discovered_exec"
    discovered_home="$(read_env_value_maybe_sudo "${SERVICE_ENV_FILE}" "DAGU_HOME")"
    if [[ -z "$discovered_home" ]]; then
        discovered_home="$(read_mac_plist_string_key "${SERVICE_PLIST_FILE}" "WorkingDirectory")"
    fi
    add_uninstall_dagu_home "$discovered_home"
}

discover_skill_removals() {
    local agents_home codex_home xdg_copilot home_copilot custom_dir
    if [[ -d "${HOME}/.claude/skills/dagu" ]]; then
        add_uninstall_skill_dir "${HOME}/.claude/skills/dagu"
    fi
    agents_home="${AGENTS_HOME:-${HOME}/.agents}"
    if [[ -d "${agents_home}/skills/dagu" ]]; then
        add_uninstall_skill_dir "${agents_home}/skills/dagu"
    fi
    codex_home="${CODEX_HOME:-${HOME}/.codex}"
    if [[ -d "${codex_home}/skills/dagu" ]]; then
        add_uninstall_skill_dir "${codex_home}/skills/dagu"
    fi
    if [[ -d "${HOME}/.config/opencode/skills/dagu" ]]; then
        add_uninstall_skill_dir "${HOME}/.config/opencode/skills/dagu"
    fi
    if [[ -d "${HOME}/.gemini/skills/dagu" ]]; then
        add_uninstall_skill_dir "${HOME}/.gemini/skills/dagu"
    fi
    xdg_copilot="${XDG_CONFIG_HOME:-${HOME}}/.copilot/copilot-instructions.md"
    home_copilot="${HOME}/.copilot/copilot-instructions.md"
    if [[ -f "${xdg_copilot}" ]]; then
        add_uninstall_copilot_file "${xdg_copilot}"
    fi
    if [[ "${home_copilot}" != "${xdg_copilot}" && -f "${home_copilot}" ]]; then
        add_uninstall_copilot_file "${home_copilot}"
    fi
    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -gt 0 ]]; then
        for custom_dir in "${EXPLICIT_SKILL_DIRS[@]}"; do
            add_uninstall_skill_dir "${custom_dir%/}/dagu"
        done
    fi
}

apply_uninstall_filters() {
    local explicit_install_path
    if [[ -n "$DAGU_INSTALL_DIR" ]]; then
        explicit_install_path="${DAGU_INSTALL_DIR%/}/dagu"
        strip_matching_install_paths "$explicit_install_path"
        if [[ ${#UNINSTALL_INSTALL_PATHS[@]} -eq 0 ]]; then
            add_uninstall_install_path "$explicit_install_path"
        fi
    fi
    if [[ -n "$SERVICE_SCOPE" ]]; then
        strip_matching_service_scopes "$SERVICE_SCOPE"
        if [[ "$OS" == "macos" && "$SERVICE_SCOPE" != "user" ]]; then
            UNINSTALL_MAC_SERVICE=0
        fi
    fi
}

discover_uninstall_artifacts() {
    local requested_scope="${SERVICE_SCOPE}"
    UNINSTALL_INSTALL_PATHS=()
    UNINSTALL_DAGU_HOMES=()
    UNINSTALL_PATH_PROFILES=()
    UNINSTALL_SKILL_DIRS=()
    UNINSTALL_COPILOT_FILES=()
    UNINSTALL_SERVICE_SCOPES=()
    UNINSTALL_MAC_SERVICE=0

    if [[ "$OS" == "linux" ]]; then
        discover_linux_uninstall_scope "system"
        discover_linux_uninstall_scope "user"
    else
        discover_mac_uninstall
    fi

    if command -v dagu >/dev/null 2>&1; then
        add_uninstall_install_path "$(command -v dagu)"
    fi
    if [[ -f "${HOME}/.local/bin/dagu" ]]; then
        add_uninstall_install_path "${HOME}/.local/bin/dagu"
    fi
    if [[ -f "/usr/local/bin/dagu" ]]; then
        add_uninstall_install_path "/usr/local/bin/dagu"
    fi
    if [[ -d "${HOME}/.dagu" ]]; then
        add_uninstall_dagu_home "${HOME}/.dagu"
    fi
    if [[ -d "/var/lib/dagu" ]]; then
        add_uninstall_dagu_home "/var/lib/dagu"
    fi
    SERVICE_SCOPE="${requested_scope}"
    find_managed_path_profiles
    discover_skill_removals
    apply_uninstall_filters
}

validate_uninstall_discovery() {
    if [[ "$UNINSTALL_MODE" != "1" ]]; then
        return 0
    fi
    if [[ ${#UNINSTALL_INSTALL_PATHS[@]} -gt 1 && -z "$DAGU_INSTALL_DIR" ]]; then
        if ! is_promptable; then
            ui_error "Multiple Dagu installations were detected. Rerun with --install-dir to choose which one to remove."
            exit 1
        fi
        ui_warn "Multiple Dagu binaries were detected: $(join_with_comma "${UNINSTALL_INSTALL_PATHS[@]}")"
        if ! prompt_yes_no "Remove all detected Dagu binaries?" "yes"; then
            ui_error "Rerun with --install-dir to choose which installation to remove."
            exit 1
        fi
        UNINSTALL_MULTIPLE_INSTALLS_CONFIRMED=1
    fi
}

run_uninstall_wizard() {
    if ! is_promptable; then
        return 0
    fi
    ui_section "Uninstall options"
    if [[ ${#UNINSTALL_SKILL_DIRS[@]} -gt 0 || ${#UNINSTALL_COPILOT_FILES[@]} -gt 0 ]]; then
        if prompt_yes_no "Remove the Dagu AI skill from detected AI tools too?" "no"; then
            REMOVE_SKILL=1
        fi
    fi
    if [[ ${#UNINSTALL_DAGU_HOMES[@]} -gt 0 ]]; then
        if prompt_yes_no "Delete the detected Dagu data directory too?" "no"; then
            PURGE_DATA=1
        fi
    fi
}

show_uninstall_plan() {
    local service_desc="none"
    local data_action="keep"
    ui_section "Uninstall plan"
    if [[ "$OS" == "linux" && ${#UNINSTALL_SERVICE_SCOPES[@]} -gt 0 ]]; then
        service_desc="$(join_with_comma "${UNINSTALL_SERVICE_SCOPES[@]}")"
    elif [[ "$OS" == "macos" && "$UNINSTALL_MAC_SERVICE" == "1" ]]; then
        service_desc="user LaunchAgent"
    fi
    ui_kv "Binary paths" "$( [[ ${#UNINSTALL_INSTALL_PATHS[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_INSTALL_PATHS[@]}" || printf 'none' )"
    ui_kv "Background service" "${service_desc}"
    if [[ "$PURGE_DATA" == "1" ]]; then
        data_action="remove"
    fi
    ui_kv "Data directory" "${data_action}: $( [[ ${#UNINSTALL_DAGU_HOMES[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_DAGU_HOMES[@]}" || printf 'none detected' )"
    ui_kv "PATH cleanup" "$( [[ ${#UNINSTALL_PATH_PROFILES[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_PATH_PROFILES[@]}" || printf 'none detected' )"
    if [[ "$REMOVE_SKILL" == "1" ]]; then
        ui_kv "AI skill removal" "$( [[ ${#UNINSTALL_SKILL_DIRS[@]} -gt 0 || ${#UNINSTALL_COPILOT_FILES[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_SKILL_DIRS[@]}" "${UNINSTALL_COPILOT_FILES[@]}" || printf 'requested, but nothing detected' )"
    else
        ui_kv "AI skill removal" "keep"
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_kv "Dry run" "yes"
    fi
}

uninstall_has_anything() {
    if [[ ${#UNINSTALL_INSTALL_PATHS[@]} -gt 0 ]]; then
        return 0
    fi
    if [[ ${#UNINSTALL_SERVICE_SCOPES[@]} -gt 0 || "$UNINSTALL_MAC_SERVICE" == "1" ]]; then
        return 0
    fi
    if [[ ${#UNINSTALL_PATH_PROFILES[@]} -gt 0 ]]; then
        return 0
    fi
    if [[ ${#UNINSTALL_DAGU_HOMES[@]} -gt 0 ]]; then
        return 0
    fi
    if [[ ${#UNINSTALL_SKILL_DIRS[@]} -gt 0 || ${#UNINSTALL_COPILOT_FILES[@]} -gt 0 ]]; then
        return 0
    fi
    return 1
}

remove_managed_path_block_from_profile() {
    local profile="$1"
    local tmp
    [[ -f "$profile" ]] || return 0
    if ! grep -Fq "# >>> dagu installer >>>" "$profile" 2>/dev/null; then
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove the installer PATH block from ${profile}"
        return 0
    fi
    tmp="$(mktempfile)"
    awk '
        /^# >>> dagu installer >>>$/ { skip=1; next }
        /^# <<< dagu installer <<<$/{ skip=0; next }
        !skip { print }
    ' "$profile" >"${tmp}"
    cat "${tmp}" >"${profile}"
}

is_unsafe_delete_target() {
    local target="${1%/}"
    case "$target" in
        ""|"/"|"/usr"|"/usr/local"|"/usr/local/bin"|"/var"|"/var/lib"|"$HOME"|"$HOME/.local"|"$HOME/.local/bin"|"$HOME/.config"|"$HOME/.claude"|"$HOME/.agents"|"$HOME/.codex"|"$HOME/.config/opencode"|"$HOME/.gemini"|"$HOME/.copilot"|"$HOME/Library"|"$HOME/Library/LaunchAgents"|"$HOME/Library/Logs")
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

remove_binary_path() {
    local path="$1"
    local parent
    [[ -z "$path" ]] && return 0
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove ${path}"
        return 0
    fi
    sudo_if_needed rm -f "${path}"
    parent="$(dirname "${path}")"
    if ! is_unsafe_delete_target "${parent}"; then
        sudo_if_needed rmdir "${parent}" >/dev/null 2>&1 || true
    fi
}

remove_linux_service_scope() {
    local scope="$1"
    SERVICE_SCOPE="$scope"
    linux_service_files
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove the Linux ${scope} service"
        return 0
    fi
    if [[ "$scope" == "system" ]]; then
        sudo systemctl stop dagu >/dev/null 2>&1 || true
        sudo systemctl disable dagu >/dev/null 2>&1 || true
        sudo rm -f "${SERVICE_UNIT_FILE}" "${SERVICE_ENV_FILE}" "${SERVICE_BOOTSTRAP_FILE}"
        sudo rmdir "$(dirname "${SERVICE_ENV_FILE}")" >/dev/null 2>&1 || true
        sudo systemctl daemon-reload >/dev/null 2>&1 || true
    else
        systemctl --user stop dagu >/dev/null 2>&1 || true
        systemctl --user disable dagu >/dev/null 2>&1 || true
        rm -f "${SERVICE_UNIT_FILE}" "${SERVICE_ENV_FILE}" "${SERVICE_BOOTSTRAP_FILE}"
        rmdir "$(dirname "${SERVICE_UNIT_FILE}")" >/dev/null 2>&1 || true
        rmdir "$(dirname "${SERVICE_ENV_FILE}")" >/dev/null 2>&1 || true
        systemctl --user daemon-reload >/dev/null 2>&1 || true
    fi
}

remove_mac_service_artifacts() {
    mac_service_files
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove the macOS LaunchAgent"
        return 0
    fi
    launchctl bootout "$(mac_launchctl_target)" >/dev/null 2>&1 || true
    rm -f "${SERVICE_PLIST_FILE}" "${SERVICE_ENV_FILE}" "${SERVICE_BOOTSTRAP_FILE}"
    rmdir "$(dirname "${SERVICE_PLIST_FILE}")" >/dev/null 2>&1 || true
    rmdir "$(dirname "${SERVICE_ENV_FILE}")" >/dev/null 2>&1 || true
}

remove_skill_dir() {
    local dir="$1"
    [[ -z "$dir" ]] && return 0
    if [[ "$(basename "$dir")" != "dagu" ]]; then
        ui_warn "Skipping unexpected skill path: ${dir}"
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove ${dir}"
        return 0
    fi
    rm -rf "${dir}"
}

remove_copilot_markers() {
    local file="$1"
    local begin_count end_count begin_line end_line tmp
    [[ -f "$file" ]] || return 0
    begin_count="$(grep -c '<!-- BEGIN DAGU -->' "$file" 2>/dev/null || true)"
    end_count="$(grep -c '<!-- END DAGU -->' "$file" 2>/dev/null || true)"
    [[ -z "$begin_count" ]] && begin_count="0"
    [[ -z "$end_count" ]] && end_count="0"
    if [[ "$begin_count" != "1" || "$end_count" != "1" ]]; then
        if [[ "$begin_count" != "0" || "$end_count" != "0" ]]; then
            ui_warn "Skipping malformed Copilot instructions file: ${file}"
        fi
        return 0
    fi
    begin_line="$(grep -n '<!-- BEGIN DAGU -->' "$file" | head -n1 | cut -d: -f1)"
    end_line="$(grep -n '<!-- END DAGU -->' "$file" | head -n1 | cut -d: -f1)"
    if [[ -z "$begin_line" || -z "$end_line" || "$end_line" -le "$begin_line" ]]; then
        ui_warn "Skipping malformed Copilot instructions file: ${file}"
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove the Dagu section from ${file}"
        return 0
    fi
    tmp="$(mktempfile)"
    awk '
        /<!-- BEGIN DAGU -->/ { skip=1; next }
        /<!-- END DAGU -->/ { skip=0; next }
        !skip { print }
    ' "$file" >"${tmp}"
    if [[ ! -s "${tmp}" ]]; then
        rm -f "${file}"
        return 0
    fi
    cat "${tmp}" >"${file}"
}

purge_dagu_home() {
    local dir="$1"
    [[ -z "$dir" ]] && return 0
    if is_unsafe_delete_target "$dir"; then
        ui_warn "Skipping unsafe data directory removal: ${dir}"
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would remove ${dir}"
        return 0
    fi
    sudo_if_needed rm -rf "${dir}"
}

run_uninstall() {
    local path scope profile skill_dir copilot_file
    if ! uninstall_has_anything; then
        ui_section "Uninstall"
        ui_info "Nothing to uninstall. No Dagu install, service, managed PATH block, data directory, or skill install was detected."
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_success "Dry run complete. No changes were made."
        return 0
    fi
    if [[ "$OS" == "linux" ]]; then
        for scope in "${UNINSTALL_SERVICE_SCOPES[@]:-}"; do
            remove_linux_service_scope "$scope"
        done
    elif [[ "$UNINSTALL_MAC_SERVICE" == "1" ]]; then
        remove_mac_service_artifacts
    fi
    for path in "${UNINSTALL_INSTALL_PATHS[@]:-}"; do
        remove_binary_path "$path"
    done
    for profile in "${UNINSTALL_PATH_PROFILES[@]:-}"; do
        remove_managed_path_block_from_profile "$profile"
    done
    if [[ "$REMOVE_SKILL" == "1" ]]; then
        for skill_dir in "${UNINSTALL_SKILL_DIRS[@]:-}"; do
            remove_skill_dir "$skill_dir"
        done
        for copilot_file in "${UNINSTALL_COPILOT_FILES[@]:-}"; do
            remove_copilot_markers "$copilot_file"
        done
    fi
    if [[ "$PURGE_DATA" == "1" ]]; then
        for path in "${UNINSTALL_DAGU_HOMES[@]:-}"; do
            purge_dagu_home "$path"
        done
    fi
}

print_uninstall_summary() {
    ui_section "Uninstall complete"
    ui_kv "Removed binaries" "$( [[ ${#UNINSTALL_INSTALL_PATHS[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_INSTALL_PATHS[@]}" || printf 'none' )"
    if [[ "$OS" == "linux" ]]; then
        ui_kv "Removed services" "$( [[ ${#UNINSTALL_SERVICE_SCOPES[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_SERVICE_SCOPES[@]}" || printf 'none' )"
    elif [[ "$OS" == "macos" ]]; then
        ui_kv "Removed service" "$( [[ "$UNINSTALL_MAC_SERVICE" == "1" ]] && printf 'user LaunchAgent' || printf 'none' )"
    fi
    ui_kv "PATH cleanup" "$( [[ ${#UNINSTALL_PATH_PROFILES[@]} -gt 0 ]] && join_with_comma "${UNINSTALL_PATH_PROFILES[@]}" || printf 'none' )"
    ui_kv "Data directory" "$( [[ "$PURGE_DATA" == "1" ]] && printf 'removed' || printf 'kept' )"
    ui_kv "AI skill" "$( [[ "$REMOVE_SKILL" == "1" ]] && printf 'removed where found' || printf 'kept' )"
}

verify_release_archive() {
    local tmpdir="$1"
    local asset="$2"
    local checksums_file="${tmpdir}/checksums.txt"
    local filtered="${tmpdir}/checksums-${asset}.txt"

    download_file "${RELEASES_URL}/download/${VERSION}/checksums.txt" "${checksums_file}"
    grep " ${asset}\$" "${checksums_file}" >"${filtered}" 2>/dev/null || true
    if [[ ! -s "${filtered}" ]]; then
        ui_error "Could not find checksum entry for ${asset}."
        exit 1
    fi
    if ! (cd "${tmpdir}" && verify_sha256sum_file "$(basename "${filtered}")"); then
        ui_error "Checksum verification failed for ${asset}."
        exit 1
    fi
}

download_dagu_archive() {
    local tmpdir="$1"
    local asset="dagu_${VERSION#v}_${ARCHIVE_OS}_${ARCH}.tar.gz"
    local archive="${tmpdir}/${asset}"
    ui_info "Downloading Dagu ${VERSION}" >&2
    download_file "${RELEASES_URL}/download/${VERSION}/${asset}" "${archive}"
    verify_release_archive "${tmpdir}" "${asset}"
    printf '%s\n' "${archive}"
}

extract_binary_from_archive() {
    local archive="$1"
    local tmpdir="$2"
    tar -xzf "${archive}" -C "${tmpdir}"
    if [[ ! -f "${tmpdir}/dagu" ]]; then
        ui_error "The Dagu binary was not found in the release archive."
        exit 1
    fi
    printf '%s\n' "${tmpdir}/dagu"
}

sudo_if_needed() {
    if "$@"; then
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
        return $?
    fi
    return 1
}

install_binary() {
    local source_binary="$1"
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would install ${source_binary} to ${INSTALL_PATH}"
        return 0
    fi

    mkdir -p "${DAGU_INSTALL_DIR}" 2>/dev/null || true
    if install -m 0755 "${source_binary}" "${INSTALL_PATH}" 2>/dev/null; then
        return 0
    fi

    ui_info "Using sudo to install into ${DAGU_INSTALL_DIR}"
    sudo mkdir -p "${DAGU_INSTALL_DIR}"
    sudo install -m 0755 "${source_binary}" "${INSTALL_PATH}"
}

managed_path_block() {
    cat <<EOF
# >>> dagu installer >>>
export PATH="\$PATH:${DAGU_INSTALL_DIR}"
# <<< dagu installer <<<
EOF
}

detect_profile_file() {
    local shell_name
    shell_name="$(basename "${SHELL:-}")"
    case "$shell_name" in
        zsh) printf '%s/.zprofile\n' "$HOME" ;;
        bash)
            if [[ -f "${HOME}/.bash_profile" ]]; then
                printf '%s/.bash_profile\n' "$HOME"
            else
                printf '%s/.bashrc\n' "$HOME"
            fi
            ;;
        *) printf '%s/.profile\n' "$HOME" ;;
    esac
}

ensure_path_block() {
    local profile
    if printf '%s' "${PATH}" | tr ':' '\n' | grep -Fxq "${DAGU_INSTALL_DIR}"; then
        return 0
    fi
    profile="$(detect_profile_file)"
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would add ${DAGU_INSTALL_DIR} to ${profile}"
        return 0
    fi
    touch "${profile}"
    if grep -Fq "# >>> dagu installer >>>" "${profile}" 2>/dev/null; then
        return 0
    fi
    printf '\n%s\n' "$(managed_path_block)" >>"${profile}"
    ui_success "Added ${DAGU_INSTALL_DIR} to ${profile}"
}

linux_service_files() {
    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        SERVICE_UNIT_FILE="/etc/systemd/system/dagu.service"
        SERVICE_ENV_FILE="/etc/dagu/dagu.env"
        SERVICE_BOOTSTRAP_FILE="/etc/dagu/bootstrap.env"
    else
        SERVICE_UNIT_FILE="${HOME}/.config/systemd/user/dagu.service"
        SERVICE_ENV_FILE="${HOME}/.config/dagu/dagu.env"
        SERVICE_BOOTSTRAP_FILE="${HOME}/.config/dagu/bootstrap.env"
    fi
}

write_linux_env_file() {
    local target="$1"
    local bootstrap="$2"
    local tmp
    tmp="$(mktempfile)"
    {
        printf 'DAGU_HOME=%s\n' "$(quote_env_value "${DAGU_HOME_DIR}")"
        printf 'DAGU_HOST=%s\n' "$(quote_env_value "${HOST}")"
        printf 'DAGU_PORT=%s\n' "$(quote_env_value "${PORT}")"
        printf 'PATH=%s\n' "$(quote_env_value "${SERVICE_PATH}")"
        if [[ "$bootstrap" == "yes" ]]; then
            printf 'DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=%s\n' "$(quote_env_value "${ADMIN_USERNAME}")"
            printf 'DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=%s\n' "$(quote_env_value "${ADMIN_PASSWORD}")"
        fi
    } >"${tmp}"

    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        sudo mkdir -p "$(dirname "${target}")"
        sudo install -m 0600 "${tmp}" "${target}"
    else
        mkdir -p "$(dirname "${target}")"
        install -m 0600 "${tmp}" "${target}"
    fi
}

write_linux_unit() {
    local tmp
    local wanted_by="default.target"
    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        wanted_by="multi-user.target"
    fi
    tmp="$(mktempfile)"
    cat >"${tmp}" <<EOF
[Unit]
Description=Dagu Workflow Engine
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_PATH} start-all
WorkingDirectory=${DAGU_HOME_DIR}
EnvironmentFile=${SERVICE_ENV_FILE}
EnvironmentFile=-${SERVICE_BOOTSTRAP_FILE}
Restart=always
RestartSec=10
EOF
    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        cat >>"${tmp}" <<EOF
User=${INSTALL_OWNER}
Group=${INSTALL_GROUP}
EOF
    fi
    cat >>"${tmp}" <<EOF

[Install]
WantedBy=${wanted_by}
EOF
    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        sudo mkdir -p "$(dirname "${SERVICE_UNIT_FILE}")"
        if [[ -f "${SERVICE_UNIT_FILE}" ]]; then
            sudo cp "${SERVICE_UNIT_FILE}" "${SERVICE_UNIT_FILE}.$(date +%Y%m%d%H%M%S).bak"
        fi
        sudo install -m 0644 "${tmp}" "${SERVICE_UNIT_FILE}"
    else
        mkdir -p "$(dirname "${SERVICE_UNIT_FILE}")"
        if [[ -f "${SERVICE_UNIT_FILE}" ]]; then
            cp "${SERVICE_UNIT_FILE}" "${SERVICE_UNIT_FILE}.$(date +%Y%m%d%H%M%S).bak"
        fi
        install -m 0644 "${tmp}" "${SERVICE_UNIT_FILE}"
    fi
}

linux_service_ctl() {
    local action="$1"
    if [[ "$SERVICE_SCOPE" == "system" ]]; then
        sudo systemctl "${action}" dagu
    else
        systemctl --user "${action}" dagu
    fi
}

maybe_enable_linux_linger() {
    if [[ "$SERVICE_SCOPE" != "user" ]]; then
        return 0
    fi
    if ! command -v loginctl >/dev/null 2>&1; then
        return 0
    fi
    if ! is_promptable; then
        return 0
    fi
    if prompt_yes_no "Keep the Dagu background service running even after you log out?" "yes"; then
        if command -v sudo >/dev/null 2>&1; then
            sudo loginctl enable-linger "${INSTALL_OWNER}" >/dev/null 2>&1 || ui_warn "Could not enable linger automatically."
        fi
    fi
}

mac_service_files() {
    SERVICE_PLIST_FILE="${HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist"
    SERVICE_ENV_FILE="${HOME}/.config/dagu/dagu.env"
    SERVICE_BOOTSTRAP_FILE="${HOME}/.config/dagu/bootstrap.env"
}

write_mac_env_file() {
    local target="$1"
    local bootstrap="$2"
    mkdir -p "$(dirname "${target}")"
    {
        printf 'DAGU_HOME=%s\n' "$(quote_env_value "${DAGU_HOME_DIR}")"
        printf 'DAGU_HOST=%s\n' "$(quote_env_value "${HOST}")"
        printf 'DAGU_PORT=%s\n' "$(quote_env_value "${PORT}")"
        printf 'PATH=%s\n' "$(quote_env_value "${SERVICE_PATH}")"
        if [[ "$bootstrap" == "yes" ]]; then
            printf 'DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=%s\n' "$(quote_env_value "${ADMIN_USERNAME}")"
            printf 'DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=%s\n' "$(quote_env_value "${ADMIN_PASSWORD}")"
        fi
    } >"${target}"
    chmod 0600 "${target}"
}

read_env_value() {
    local file="$1"
    local key="$2"
    env -i KEY_NAME="${key}" sh -c '
        . "$1" >/dev/null 2>&1 || exit 1
        eval "printf %s \"\${$KEY_NAME}\""
    ' sh "${file}" 2>/dev/null || true
}

write_mac_plist() {
    local logs_dir="${HOME}/Library/Logs/Dagu"
    local bootstrap_user="" bootstrap_pass="" service_path="" tmp
    mkdir -p "${logs_dir}" "$(dirname "${SERVICE_PLIST_FILE}")"
    if [[ -f "${SERVICE_BOOTSTRAP_FILE}" ]]; then
        bootstrap_user="$(read_env_value "${SERVICE_BOOTSTRAP_FILE}" "DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME")"
        bootstrap_pass="$(read_env_value "${SERVICE_BOOTSTRAP_FILE}" "DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD")"
    fi
    if [[ -f "${SERVICE_ENV_FILE}" ]]; then
        service_path="$(read_env_value "${SERVICE_ENV_FILE}" "PATH")"
    fi
    if [[ -z "${service_path}" ]]; then
        service_path="${SERVICE_PATH}"
    fi
    tmp="$(mktempfile)"
    cat >"${tmp}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$(xml_escape "${SERVICE_LABEL}")</string>
  <key>ProgramArguments</key>
  <array>
    <string>$(xml_escape "${INSTALL_PATH}")</string>
    <string>start-all</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$(xml_escape "${DAGU_HOME_DIR}")</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$(xml_escape "${logs_dir}/dagu.out.log")</string>
  <key>StandardErrorPath</key>
  <string>$(xml_escape "${logs_dir}/dagu.err.log")</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>DAGU_HOME</key>
    <string>$(xml_escape "${DAGU_HOME_DIR}")</string>
    <key>DAGU_HOST</key>
    <string>$(xml_escape "${HOST}")</string>
    <key>DAGU_PORT</key>
    <string>$(xml_escape "${PORT}")</string>
    <key>PATH</key>
    <string>$(xml_escape "${service_path}")</string>
EOF
    if [[ -n "${bootstrap_user}" ]]; then
        cat >>"${tmp}" <<EOF
    <key>DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME</key>
    <string>$(xml_escape "${bootstrap_user}")</string>
    <key>DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD</key>
    <string>$(xml_escape "${bootstrap_pass}")</string>
EOF
    fi
    cat >>"${tmp}" <<'EOF'
  </dict>
</dict>
</plist>
EOF
    if [[ -f "${SERVICE_PLIST_FILE}" ]]; then
        cp "${SERVICE_PLIST_FILE}" "${SERVICE_PLIST_FILE}.$(date +%Y%m%d%H%M%S).bak"
    fi
    install -m 0644 "${tmp}" "${SERVICE_PLIST_FILE}"
}

remove_bootstrap_file() {
    if [[ ! -f "${SERVICE_BOOTSTRAP_FILE}" ]]; then
        return 0
    fi
    if [[ "$OS" == "linux" && "$SERVICE_SCOPE" == "system" ]]; then
        sudo rm -f "${SERVICE_BOOTSTRAP_FILE}"
    else
        rm -f "${SERVICE_BOOTSTRAP_FILE}"
    fi
}

backup_bootstrap_file() {
    local backup
    if [[ ! -f "${SERVICE_BOOTSTRAP_FILE}" ]]; then
        return 0
    fi
    backup="$(mktempfile)"
    if [[ "$OS" == "linux" && "$SERVICE_SCOPE" == "system" ]]; then
        sudo cp "${SERVICE_BOOTSTRAP_FILE}" "${backup}"
    else
        cp "${SERVICE_BOOTSTRAP_FILE}" "${backup}"
    fi
    printf '%s\n' "${backup}"
}

restore_bootstrap_credentials() {
    if [[ -z "${SCRUB_BACKUP_FILE}" || ! -f "${SCRUB_BACKUP_FILE}" ]]; then
        return 0
    fi
    if [[ "$OS" == "linux" && "$SERVICE_SCOPE" == "system" ]]; then
        sudo install -m 0600 "${SCRUB_BACKUP_FILE}" "${SERVICE_BOOTSTRAP_FILE}"
    else
        install -m 0600 "${SCRUB_BACKUP_FILE}" "${SERVICE_BOOTSTRAP_FILE}"
    fi
    if [[ "$OS" == "macos" ]]; then
        write_mac_plist
    fi
    start_service >/dev/null 2>&1 || true
}

mac_launchctl_target() {
    printf 'gui/%s/%s\n' "$(id -u)" "${SERVICE_LABEL}"
}

mac_service_restart() {
    local target
    local attempt
    target="$(mac_launchctl_target)"
    launchctl bootout "${target}" >/dev/null 2>&1 || true
    for attempt in $(seq 1 10); do
        if launchctl bootstrap "gui/$(id -u)" "${SERVICE_PLIST_FILE}" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    ui_error "Could not load the LaunchAgent ${SERVICE_LABEL}."
    return 1
}

write_service_files() {
    if [[ "$SERVICE_MODE" != "yes" ]]; then
        return 0
    fi
    if [[ "$OS" == "linux" ]]; then
        linux_service_files
        if [[ "$DRY_RUN" == "1" ]]; then
            ui_info "Would write ${SERVICE_UNIT_FILE}, ${SERVICE_ENV_FILE}, and ${SERVICE_BOOTSTRAP_FILE}"
            return 0
        fi
        if [[ "$SERVICE_SCOPE" == "system" ]]; then
            sudo mkdir -p "${DAGU_HOME_DIR}"
            sudo chown "${INSTALL_OWNER}:${INSTALL_GROUP}" "${DAGU_HOME_DIR}" >/dev/null 2>&1 || true
        else
            mkdir -p "${DAGU_HOME_DIR}"
        fi
        write_linux_env_file "${SERVICE_ENV_FILE}" "no"
        if has_admin_bootstrap; then
            write_linux_env_file "${SERVICE_BOOTSTRAP_FILE}" "yes"
        else
            remove_bootstrap_file
        fi
        write_linux_unit
        maybe_enable_linux_linger
    else
        mac_service_files
        if [[ "$DRY_RUN" == "1" ]]; then
            ui_info "Would write ${SERVICE_PLIST_FILE} and bootstrap environment files"
            return 0
        fi
        mkdir -p "${DAGU_HOME_DIR}"
        write_mac_env_file "${SERVICE_ENV_FILE}" "no"
        if has_admin_bootstrap; then
            write_mac_env_file "${SERVICE_BOOTSTRAP_FILE}" "yes"
        else
            remove_bootstrap_file
        fi
        write_mac_plist
    fi
}

start_service() {
    if [[ "$SERVICE_MODE" != "yes" ]]; then
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would start the Dagu service"
        return 0
    fi
    if [[ "$OS" == "linux" ]]; then
        if [[ "$SERVICE_SCOPE" == "system" ]]; then
            sudo systemctl daemon-reload
            sudo systemctl enable dagu >/dev/null 2>&1 || true
        else
            systemctl --user daemon-reload
            systemctl --user enable dagu >/dev/null 2>&1 || true
        fi
        linux_service_ctl restart
    else
        mac_service_restart
    fi
}

stop_service() {
    if [[ "$SERVICE_MODE" != "yes" || "$DRY_RUN" == "1" ]]; then
        return 0
    fi
    if [[ "$OS" == "linux" ]]; then
        linux_service_ctl stop || true
    else
        launchctl bootout "$(mac_launchctl_target)" >/dev/null 2>&1 || true
    fi
}

service_status_hint() {
    if [[ "$SERVICE_MODE" != "yes" ]]; then
        return 0
    fi
    if [[ "$OS" == "linux" ]]; then
        if [[ "$SERVICE_SCOPE" == "system" ]]; then
            printf 'sudo systemctl status dagu\n'
        else
            printf 'systemctl --user status dagu\n'
        fi
    else
        printf 'launchctl print %s\n' "$(mac_launchctl_target)"
    fi
}

wait_for_health() {
    local tries="${1:-60}"
    local url="${SERVICE_URL}/api/v1/health"
    local i
    if [[ "$DRY_RUN" == "1" ]]; then
        return 0
    fi
    for i in $(seq 1 "${tries}"); do
        detect_downloader
        if [[ "$DOWNLOADER" == "curl" ]]; then
            if curl -fsS "${url}" >/dev/null 2>&1; then
                return 0
            fi
        elif wget -qO- "${url}" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

verify_login() {
    local payload response
    payload=$(printf '{"username":"%s","password":"%s"}' "$(json_escape "${ADMIN_USERNAME}")" "$(json_escape "${ADMIN_PASSWORD}")")
    if [[ "$DRY_RUN" == "1" ]]; then
        return 0
    fi
    if [[ "$DOWNLOADER" == "curl" ]]; then
        response="$(curl -fsS -H 'Content-Type: application/json' -d "${payload}" "${SERVICE_URL}/api/v1/auth/login" 2>/dev/null || true)"
    else
        response="$(wget -qO- --header='Content-Type: application/json' --post-data="${payload}" "${SERVICE_URL}/api/v1/auth/login" 2>/dev/null || true)"
    fi
    printf '%s' "${response}" | grep -q '"token"'
}

scrub_bootstrap_credentials() {
    if [[ "$SERVICE_MODE" != "yes" || "$DRY_RUN" == "1" ]]; then
        return 0
    fi
    SCRUB_BACKUP_FILE="$(backup_bootstrap_file)"
    remove_bootstrap_file
    if [[ "$OS" == "macos" ]]; then
        write_mac_plist
    fi
    if ! start_service; then
        restore_bootstrap_credentials
        return 1
    fi
}

verify_bootstrap_flow() {
    if [[ "$SERVICE_MODE" != "yes" ]]; then
        return 0
    fi
    if ! has_admin_bootstrap; then
        ui_warn "No initial admin credentials were provided. Open ${SERVICE_URL}/setup to finish the first-time setup."
        return 0
    fi
    if ! wait_for_health 60; then
        ui_error "Dagu did not become healthy."
        ui_warn "Check the service status with: $(service_status_hint)"
        return 1
    fi
    if ! verify_login; then
        ui_error "Dagu started, but the admin login bootstrap did not verify."
        ui_warn "The bootstrap credentials were left in place so you can rerun the installer safely."
        return 1
    fi
    if ! scrub_bootstrap_credentials; then
        ui_error "Dagu could not restart after removing the bootstrap credentials."
        ui_warn "The bootstrap credentials were restored so you can retry safely."
        return 1
    fi
    if ! wait_for_health 60; then
        ui_error "Dagu did not come back after removing the bootstrap credentials."
        restore_bootstrap_credentials
        ui_warn "The bootstrap credentials were restored so you can retry safely."
        return 1
    fi
    if ! verify_login; then
        ui_error "The admin login no longer works after the bootstrap cleanup."
        restore_bootstrap_credentials
        ui_warn "The bootstrap credentials were restored so you can retry safely."
        return 1
    fi
    SCRUB_BACKUP_FILE=""
    return 0
}

install_skill() {
    local custom_dir
    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -eq 0 && "${SKILL_MODE:-skip}" != "auto" ]]; then
        return 0
    fi
    if [[ "$DRY_RUN" == "1" ]]; then
        ui_info "Would install the Dagu AI skill with the shared skills installer"
        return 0
    fi
    if [[ ${#EXPLICIT_SKILL_DIRS[@]} -gt 0 ]]; then
        for custom_dir in "${EXPLICIT_SKILL_DIRS[@]}"; do
            ui_warn "--skills-dir is no longer supported by the Dagu installer: ${custom_dir}"
        done
        ui_warn "Install the skill with: gh skill install dagucloud/dagu dagu"
        return 0
    fi
    if command -v gh >/dev/null 2>&1; then
        if ! gh skill install dagucloud/dagu dagu; then
            ui_warn "Failed to install the Dagu AI skill. Install manually with: gh skill install dagucloud/dagu dagu"
        fi
        return 0
    fi
    if command -v npx >/dev/null 2>&1; then
        if ! npx skills add https://github.com/dagucloud/dagu --skill dagu; then
            ui_warn "Failed to install the Dagu AI skill. Install manually with: npx skills add https://github.com/dagucloud/dagu --skill dagu"
        fi
        return 0
    fi
    ui_warn "No shared skills installer was found. Install manually with: gh skill install dagucloud/dagu dagu"
}

open_browser_if_requested() {
    if [[ "$OPEN_BROWSER" != "yes" || "$DRY_RUN" == "1" ]]; then
        return 0
    fi
    if ! is_promptable; then
        return 0
    fi
    if ! prompt_yes_no "Open Dagu in your browser now?" "yes"; then
        return 0
    fi
    if [[ "$OS" == "macos" && -x /usr/bin/open ]]; then
        /usr/bin/open "${SERVICE_URL}" >/dev/null 2>&1 || true
    elif command -v xdg-open >/dev/null 2>&1; then
        xdg-open "${SERVICE_URL}" >/dev/null 2>&1 || true
    fi
}

print_summary() {
    ui_section "Success"
    ui_kv "Installed" "${INSTALL_PATH}"
    if [[ "$SERVICE_MODE" == "yes" ]]; then
        ui_kv "Service URL" "${SERVICE_URL}"
        ui_kv "Status command" "$(service_status_hint)"
    fi
    if [[ "$SERVICE_MODE" == "yes" && -n "${ADMIN_USERNAME}" ]]; then
        ui_kv "Admin username" "${ADMIN_USERNAME}"
    elif [[ "$SERVICE_MODE" == "yes" ]]; then
        ui_kv "First-time setup" "${SERVICE_URL}/setup"
    fi
    ui_kv "Skill install" "$( [[ ${#EXPLICIT_SKILL_DIRS[@]} -gt 0 || "${SKILL_MODE:-skip}" == "auto" ]] && printf 'configured' || printf 'skipped' )"
}

main() {
    bootstrap_gum_temp || true
    print_banner
    detect_os_arch
    detect_skill_targets
    choose_operation_mode

    if [[ "$UNINSTALL_MODE" == "1" ]]; then
        validate_uninstall_flags
        discover_uninstall_artifacts
        validate_uninstall_discovery
        run_uninstall_wizard
        show_uninstall_plan
        run_uninstall
        if uninstall_has_anything; then
            print_uninstall_summary
        fi
        exit 0
    fi

    resolve_version
    configure_defaults
    run_install_wizard
    configure_defaults
    validate_admin_bootstrap
    show_install_plan

    if [[ "$DRY_RUN" == "1" ]]; then
        ui_success "Dry run complete. No changes were made."
        exit 0
    fi

    local tmpdir archive binary
    tmpdir="$(mktempdir)"

    archive="$(download_dagu_archive "${tmpdir}")"
    binary="$(extract_binary_from_archive "${archive}" "${tmpdir}")"

    install_binary "${binary}"
    ensure_path_block || true

    if [[ "$SERVICE_MODE" == "yes" ]]; then
        write_service_files
        start_service
        verify_bootstrap_flow
    fi

    install_skill
    print_summary
    open_browser_if_requested
}

main "$@"
