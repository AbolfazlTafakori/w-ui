#!/bin/bash

# W-UI management script.
#
# Everything an operator does to the panel from a terminal lives here: install,
# upgrade, service control, settings, and the diagnostics that answer "why is a
# customer not connecting". Anything that needs the database goes through the
# panel binary rather than reading SQLite from a shell, so the schema has exactly
# one implementation.

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
cyan='\033[0;36m'
bold='\033[1m'
plain='\033[0m'

BIN_PATH=/usr/local/bin/wui
DATA_DIR=/var/lib/wui
CONF_DIR=/etc/wui
ENV_FILE=$CONF_DIR/wui.env
SERVICE=wui
SERVICE_USER=wui
REPO_RAW=https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main

function LOGD() { echo -e "${yellow}[DEG] $* ${plain}"; }
function LOGE() { echo -e "${red}[ERR] $* ${plain}"; }
function LOGI() { echo -e "${green}[INF] $* ${plain}"; }

[[ $EUID -ne 0 ]] && LOGE "Please run this script as root" && exit 1

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -rp "$1 [Default $2]: " temp
        if [[ "${temp}" == "" ]]; then
            temp=$2
        fi
    else
        read -rp "$1 [y/n]: " temp
    fi
    if [[ "${temp}" == "y" || "${temp}" == "Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "Restart the panel? Connected customers are not affected" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}Press enter to return to the main menu: ${plain}" && read -r temp
    show_menu
}

have() { command -v "$1" > /dev/null 2>&1; }

# ── the box ──────────────────────────────────────────────────────────────────
#
# Rows are padded here rather than by hand. An emoji occupies two terminal
# columns while counting as one character, so a hand-aligned menu drifts the
# moment a line is edited.

BOX_W=48

# The rule is built by repetition rather than with tr, which substitutes byte by
# byte and would emit the first third of this three-byte character 48 times.
BOX_RULE=$(printf '─%.0s' $(seq 1 $BOX_W))

box_top() { printf '╔%s╗\n' "$BOX_RULE"; }
box_mid() { printf '│%s│\n' "$BOX_RULE"; }
box_end() { printf '╚%s╝\n' "$BOX_RULE"; }

# box_row_plain <number> <label> — a row with no emoji.
box_row_plain() {
    local num="$1" label="$2"
    # 2 leading spaces + "NN." + space
    local pad=$((BOX_W - 6 - ${#label}))
    [[ $pad -lt 0 ]] && pad=0
    printf "│  ${green}%2s.${plain} %s%*s│\n" "$num" "$label" "$pad" ''
}

# box_row <emoji> <number> <label>
box_row() {
    local emoji="$1" num="$2" label="$3"
    # 2 leading spaces + emoji (2 columns) + space + "NN." + space
    local pad=$((BOX_W - 9 - ${#label}))
    [[ $pad -lt 0 ]] && pad=0
    printf "│  %s ${green}%2s.${plain} %s%*s│\n" "$emoji" "$num" "$label" "$pad" ''
}

# box_title <text>
box_title() {
    local pad=$((BOX_W - 2 - ${#1}))
    [[ $pad -lt 0 ]] && pad=0
    printf "│  ${green}${bold}%s${plain}%*s│\n" "$1" "$pad" ''
}

# ── install state ────────────────────────────────────────────────────────────

check_installed() { [[ -f "$BIN_PATH" ]]; }

check_install() {
    if ! check_installed; then
        LOGE "The panel is not installed. Install it first."
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    fi
    return 0
}

check_uninstall() {
    if check_installed; then
        LOGE "The panel is already installed. Uninstall it first."
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    fi
    return 0
}

# check_status: 0 running, 1 installed but stopped, 2 not installed
check_status() {
    check_installed || return 2
    if systemctl is-active --quiet "$SERVICE" 2> /dev/null; then
        return 0
    fi
    return 1
}

check_enabled() { systemctl is-enabled --quiet "$SERVICE" 2> /dev/null; }

show_enable_status() {
    if check_enabled; then
        echo -e "Autostart:   ${green}Yes${plain}"
    else
        echo -e "Autostart:   ${red}No${plain}"
    fi
}

# The engine line is the one that matters commercially: without a kernel that
# can hold quota objects, a data limit is a suggestion rather than a limit.
show_engine_status() {
    if ! have nft; then
        echo -e "Enforcement: ${red}nftables missing${plain}"
        return
    fi
    modprobe nft_quota 2> /dev/null
    if nft add table inet wui_probe 2> /dev/null && nft add quota inet wui_probe q '{ over 1000 bytes }' 2> /dev/null; then
        echo -e "Enforcement: ${green}Exact (kernel quota)${plain}"
    else
        echo -e "Enforcement: ${yellow}Reduced (no nft_quota; limits applied on the next tick)${plain}"
    fi
    nft delete table inet wui_probe 2> /dev/null
}

show_protocol_status() {
    local wg="${red}not installed${plain}"
    have wg && wg="${green}$(wg --version 2> /dev/null | awk '{print $2}')${plain}"
    have awg && wg="$wg ${green}+ amnezia${plain}"

    local ovpn="${red}not installed${plain}"
    have openvpn && ovpn="${green}$(openvpn --version 2> /dev/null | head -1 | awk '{print $2}')${plain}"

    echo -e "WireGuard:   $wg"
    echo -e "OpenVPN:     $ovpn"
}

show_status() {
    check_status
    case $? in
        0)
            echo -e "Panel state: ${green}Running${plain}"
            show_enable_status
            ;;
        1)
            echo -e "Panel state: ${yellow}Not Running${plain}"
            show_enable_status
            ;;
        2)
            echo -e "Panel state: ${red}Not Installed${plain}"
            return
            ;;
    esac
    show_protocol_status
    show_engine_status
}

# ── the environment file ─────────────────────────────────────────────────────
#
# systemd reads this after its own Environment= lines, so whatever is written
# here wins. Keys are replaced by filtering and appending rather than by sed, so
# a value containing a slash or a pipe cannot corrupt the file.

get_env() {
    [[ -f "$ENV_FILE" ]] || return 1
    grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2-
}

set_env() {
    local key="$1" value="$2" tmp
    mkdir -p "$CONF_DIR"
    touch "$ENV_FILE"
    tmp=$(mktemp)
    grep -v "^${key}=" "$ENV_FILE" > "$tmp" 2> /dev/null
    echo "${key}=${value}" >> "$tmp"
    command install -m 0640 "$tmp" "$ENV_FILE"
    chgrp "$SERVICE_USER" "$ENV_FILE" 2> /dev/null
    rm -f "$tmp"
    LOGI "${key} set to ${value}"
}

unset_env() {
    [[ -f "$ENV_FILE" ]] || return 0
    local tmp
    tmp=$(mktemp)
    grep -v "^$1=" "$ENV_FILE" > "$tmp" 2> /dev/null
    command install -m 0640 "$tmp" "$ENV_FILE"
    rm -f "$tmp"
}

# panel_cli runs the binary with the same configuration the service uses, so
# what it prints is what the panel actually sees.
panel_cli() {
    (
        set -a
        WUI_DATA_DIR="$DATA_DIR"
        WUI_DB_SOURCE="$DATA_DIR/wui.db"
        [[ -f "$ENV_FILE" ]] && . "$ENV_FILE"
        set +a
        "$BIN_PATH" "$@"
    )
}

panel_port() {
    local listen
    listen=$(get_env WUI_LISTEN)
    [[ -z "$listen" ]] && listen=$(grep -oE 'WUI_LISTEN=[^ ]+' /etc/systemd/system/${SERVICE}.service 2> /dev/null | head -1 | cut -d= -f2-)
    [[ -z "$listen" ]] && listen="0.0.0.0:2096"
    echo "${listen##*:}"
}

# ── lifecycle ────────────────────────────────────────────────────────────────

install_panel() {
    LOGI "Fetching the installer"
    bash <(curl -fsSL "${REPO_RAW}/install.sh")
    if [[ $? == 0 ]]; then
        LOGI "Installed"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

update() {
    confirm "Update to the latest version? Settings and customers are kept" "y"
    if [[ $? != 0 ]]; then
        LOGE "Cancelled"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    bash <(curl -fsSL "${REPO_RAW}/install.sh")
    if [[ $? == 0 ]]; then
        LOGI "Updated; the panel has restarted"
        exit 0
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

update_menu() {
    LOGI "Updating this management script"
    if ! curl -fsSL -o /tmp/w-ui.sh "${REPO_RAW}/w-ui.sh"; then
        LOGE "Could not download the script"
        before_show_menu
        return 1
    fi
    command install -m 0755 /tmp/w-ui.sh /usr/local/bin/w-ui
    rm -f /tmp/w-ui.sh
    LOGI "Updated. Run w-ui again."
    exit 0
}

uninstall() {
    confirm "Remove the panel? Customers stop being served" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    bash <(curl -fsSL "${REPO_RAW}/install.sh") --uninstall
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

start() {
    check_status
    if [[ $? == 0 ]]; then
        LOGI "The panel is already running"
    else
        systemctl start "$SERVICE"
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            LOGI "Started"
        else
            LOGE "Failed to start; check the logs"
        fi
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    check_status
    if [[ $? == 1 ]]; then
        LOGI "The panel is already stopped"
    else
        systemctl stop "$SERVICE"
        sleep 2
        check_status
        if [[ $? == 1 ]]; then
            LOGI "Stopped"
            # Worth saying plainly: stopping the panel does not stop the VPN.
            LOGD "Tunnels keep running. Customers are still served, but limits are no longer enforced."
        else
            LOGE "Failed to stop; check the logs"
        fi
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart() {
    systemctl restart "$SERVICE"
    sleep 2
    check_status
    if [[ $? == 0 ]]; then
        LOGI "Restarted"
    else
        LOGE "Failed to restart; check the logs"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    systemctl status "$SERVICE" -l --no-pager
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    systemctl enable "$SERVICE"
    if [[ $? == 0 ]]; then
        LOGI "Autostart enabled"
    else
        LOGE "Could not enable autostart"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    systemctl disable "$SERVICE"
    if [[ $? == 0 ]]; then
        LOGI "Autostart disabled"
    else
        LOGE "Could not disable autostart"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    echo -e "${green}\t1.${plain} Follow the log"
    echo -e "${green}\t2.${plain} Last 100 lines"
    echo -e "${green}\t3.${plain} Errors only"
    echo -e "${green}\t4.${plain} OpenVPN server logs"
    echo -e "${green}\t5.${plain} ${red}Clear${plain} all logs"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0) show_menu ;;
        1)
            journalctl -u "$SERVICE" -e --no-pager -f
            [[ $# == 0 ]] && before_show_menu
            ;;
        2)
            journalctl -u "$SERVICE" -n 100 --no-pager
            [[ $# == 0 ]] && before_show_menu
            ;;
        3)
            journalctl -u "$SERVICE" -p err -n 100 --no-pager
            [[ $# == 0 ]] && before_show_menu
            ;;
        4)
            local logs
            logs=$(ls "$DATA_DIR"/openvpn/*/openvpn.log 2> /dev/null)
            if [[ -z "$logs" ]]; then
                LOGE "No OpenVPN interfaces have been created"
            else
                for f in $logs; do
                    echo -e "${green}── $f ──${plain}"
                    tail -40 "$f"
                done
            fi
            [[ $# == 0 ]] && before_show_menu
            ;;
        5)
            journalctl --rotate
            journalctl --vacuum-time=1s
            LOGI "Logs cleared"
            restart
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            show_log
            ;;
    esac
}

# ── access ───────────────────────────────────────────────────────────────────

reset_admin() {
    confirm "Reset the administrator account?" "n"
    if [[ $? != 0 ]]; then
        [[ $# == 0 ]] && show_menu
        return 0
    fi

    read -rp "New username (blank keeps the current one): " config_account
    read -rp "New password (blank generates one): " config_password

    local args=()
    [[ -n "$config_account" ]] && args+=(--username "$config_account")
    [[ -n "$config_password" ]] && args+=(--password "$config_password")

    local out
    out=$(panel_cli admin reset "${args[@]}" 2>&1)
    if [[ $? != 0 ]]; then
        LOGE "$out"
        [[ $# == 0 ]] && before_show_menu
        return 1
    fi

    echo -e "${green}───────────────────────────────────────${plain}"
    echo -e "$out" | sed 's/^/  /'
    echo -e "${green}───────────────────────────────────────${plain}"
    LOGD "Write this down. The password is not recoverable."
    [[ $# == 0 ]] && before_show_menu
}

set_port() {
    echo && echo -n -e "Enter the panel port [1-65535]: " && read -r port
    if [[ -z "${port}" ]]; then
        LOGD "Cancelled"
        before_show_menu
        return
    fi
    if ! [[ "$port" =~ ^[0-9]+$ ]] || [[ "$port" -lt 1 || "$port" -gt 65535 ]]; then
        LOGE "$port is not a valid port"
        before_show_menu
        return
    fi

    local bind
    bind=$(get_env WUI_LISTEN)
    [[ -z "$bind" ]] && bind="0.0.0.0:2096"
    set_env WUI_LISTEN "${bind%:*}:${port}"

    LOGI "Port set to ${port}"
    LOGD "If a firewall is running, open it: option 17 on the main menu."
    confirm_restart
}

check_config() {
    local info
    info=$(panel_cli setting show 2>&1)
    if [[ $? != 0 ]]; then
        LOGE "Could not read the settings: $info"
        before_show_menu
        return
    fi

    echo -e "${green}── Panel ──────────────────────────────${plain}"
    echo "$info" | sed 's/^/  /'

    local port server_ip
    port=$(echo "$info" | grep -E '^port:' | awk '{print $2}')
    server_ip=$(curl -fsS --max-time 4 https://api4.ipify.org 2> /dev/null)
    [[ -z "$server_ip" ]] && server_ip=$(hostname -I 2> /dev/null | awk '{print $1}')
    [[ -z "$server_ip" ]] && server_ip="your-server-ip"

    echo
    echo -e "${green}── Access ─────────────────────────────${plain}"
    local listen
    listen=$(echo "$info" | grep -E '^listen:' | awk '{print $2}')
    if [[ "$listen" == 127.0.0.1:* || "$listen" == localhost:* ]]; then
        echo -e "  ${yellow}The panel is bound to loopback and is not reachable from outside.${plain}"
        echo -e "  ${yellow}Reach it through a reverse proxy, or over an SSH tunnel:${plain}"
        echo -e "    ssh -L ${port}:127.0.0.1:${port} root@${server_ip}"
        echo -e "  then open ${green}http://127.0.0.1:${port}${plain}"
    else
        echo -e "  URL: ${green}http://${server_ip}:${port}${plain}"
        echo -e "  ${yellow}This is plain HTTP. Put it behind TLS before selling from it${plain}"
        echo -e "  ${yellow}— option 18 on the main menu.${plain}"
    fi

    echo
    echo -e "${green}── Environment file ───────────────────${plain}"
    if [[ -f "$ENV_FILE" ]]; then
        sed 's/^/  /' "$ENV_FILE"
    else
        echo -e "  ${yellow}(none; the panel is running on its defaults)${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

# ── settings ─────────────────────────────────────────────────────────────────
#
# Everything the panel reads at startup can be set here. Each option writes one
# key to the environment file and offers a restart, because none of them takes
# effect until the process re-reads its configuration.

settings_menu() {
    echo
    echo -e "${green}${bold}  Settings${plain}"
    echo -e "  Current values are shown in brackets."
    echo
    echo -e "${green}\t1.${plain} Listen address            [${cyan}$(get_env WUI_LISTEN || echo '0.0.0.0:2096 (default)')${plain}]"
    echo -e "${green}\t2.${plain} Panel port                [${cyan}$(panel_port)${plain}]"
    echo -e "${green}\t3.${plain} Data directory            [${cyan}$(get_env WUI_DATA_DIR || echo "$DATA_DIR (default)")${plain}]"
    echo -e "${green}\t4.${plain} Collection interval       [${cyan}$(get_env WUI_COLLECT_INTERVAL || echo '2s (default)')${plain}]"
    echo -e "${green}\t5.${plain} Default language          [${cyan}$(get_env WUI_DEFAULT_LOCALE || echo 'en (default)')${plain}]"
    echo -e "${green}\t6.${plain} Log level                 [${cyan}$(get_env WUI_LOG_LEVEL || echo 'info (default)')${plain}]"
    echo -e "${green}\t7.${plain} Log format                [${cyan}$(get_env WUI_LOG_FORMAT || echo 'text (default)')${plain}]"
    echo -e "${green}\t8.${plain} Database source           [${cyan}$(get_env WUI_DB_SOURCE || echo "$DATA_DIR/wui.db (default)")${plain}]"
    echo -e "${green}\t9.${plain} Show the environment file"
    echo -e "${green}\t10.${plain} ${red}Reset${plain} every setting to its default"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0) show_menu ;;
        1) set_listen ;;
        2) set_port ;;
        3) set_data_dir ;;
        4) set_interval ;;
        5) set_locale ;;
        6) set_log_level ;;
        7) set_log_format ;;
        8) set_db_source ;;
        9)
            echo
            if [[ -f "$ENV_FILE" ]]; then
                echo -e "${green}── $ENV_FILE ──${plain}"
                sed 's/^/  /' "$ENV_FILE"
            else
                LOGD "No environment file; every setting is at its default."
            fi
            echo && read -rp "Press enter to continue: " temp
            settings_menu
            ;;
        10) reset_settings ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            settings_menu
            ;;
    esac
}

set_listen() {
    echo
    echo -e "  ${yellow}0.0.0.0:2096${plain} listens on every address."
    echo -e "  ${yellow}127.0.0.1:2096${plain} listens on loopback only, which is the safe"
    echo -e "  choice when a reverse proxy terminates TLS in front of the panel."
    echo && read -rp "Listen address [host:port]: " value
    if [[ -z "$value" ]]; then
        LOGD "Cancelled"
        settings_menu
        return
    fi
    if ! [[ "$value" =~ ^[0-9a-zA-Z.:\[\]-]+:[0-9]+$ ]]; then
        LOGE "That is not a host:port address"
        settings_menu
        return
    fi
    set_env WUI_LISTEN "$value"
    confirm_restart
}

set_data_dir() {
    echo
    echo -e "  ${yellow}This holds the database, the OpenVPN interface files and the keys.${plain}"
    echo -e "  ${yellow}Moving it does not move the existing data; copy it yourself first.${plain}"
    echo && read -rp "Data directory: " value
    if [[ -z "$value" ]]; then
        LOGD "Cancelled"
        settings_menu
        return
    fi
    if [[ "$value" != /* ]]; then
        LOGE "The path must be absolute"
        settings_menu
        return
    fi
    if [[ ! -d "$value" ]]; then
        confirm "$value does not exist. Create it?" "y"
        if [[ $? == 0 ]]; then
            command install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$value"
        else
            settings_menu
            return
        fi
    fi
    set_env WUI_DATA_DIR "$value"
    set_env WUI_DB_SOURCE "$value/wui.db"
    LOGD "The service is only allowed to write to its data directory."
    LOGD "A new location needs ReadWritePaths in the unit file to match."
    confirm_restart
}

set_interval() {
    echo
    echo -e "  How often usage is read from the kernel and written to the database."
    echo -e "  ${yellow}This does not affect how exactly data limits are enforced${plain} — the"
    echo -e "  kernel stops a customer at the byte regardless. It only changes how"
    echo -e "  fresh the numbers in the panel are, and how quickly an expired"
    echo -e "  customer is disconnected."
    echo -e "  Shorter is heavier on a server with many customers. Minimum 1s."
    echo && read -rp "Collection interval [e.g. 2s, 5s, 1m]: " value
    if [[ -z "$value" ]]; then
        LOGD "Cancelled"
        settings_menu
        return
    fi
    if ! [[ "$value" =~ ^[0-9]+(s|m|h)$ ]]; then
        LOGE "Use a duration such as 2s, 30s or 1m"
        settings_menu
        return
    fi
    set_env WUI_COLLECT_INTERVAL "$value"
    confirm_restart
}

set_locale() {
    echo
    echo -e "${green}\t1.${plain} English"
    echo -e "${green}\t2.${plain} فارسی (Persian)"
    echo -e "${green}\t0.${plain} Back"
    read -rp "Choose an option: " choice
    case "$choice" in
        0) settings_menu ;;
        1) set_env WUI_DEFAULT_LOCALE "en" && confirm_restart ;;
        2) set_env WUI_DEFAULT_LOCALE "fa" && confirm_restart ;;
        *)
            echo -e "${red}Invalid option.${plain}\n"
            set_locale
            ;;
    esac
}

set_log_level() {
    echo
    echo -e "${green}\t1.${plain} debug   ${yellow}(verbose; useful when something is wrong)${plain}"
    echo -e "${green}\t2.${plain} info    ${yellow}(the default)${plain}"
    echo -e "${green}\t3.${plain} warn"
    echo -e "${green}\t4.${plain} error"
    echo -e "${green}\t0.${plain} Back"
    read -rp "Choose an option: " choice
    case "$choice" in
        0) settings_menu ;;
        1) set_env WUI_LOG_LEVEL "debug" && confirm_restart ;;
        2) set_env WUI_LOG_LEVEL "info" && confirm_restart ;;
        3) set_env WUI_LOG_LEVEL "warn" && confirm_restart ;;
        4) set_env WUI_LOG_LEVEL "error" && confirm_restart ;;
        *)
            echo -e "${red}Invalid option.${plain}\n"
            set_log_level
            ;;
    esac
}

set_log_format() {
    echo
    echo -e "${green}\t1.${plain} text    ${yellow}(readable in a terminal)${plain}"
    echo -e "${green}\t2.${plain} json    ${yellow}(for a log collector)${plain}"
    echo -e "${green}\t0.${plain} Back"
    read -rp "Choose an option: " choice
    case "$choice" in
        0) settings_menu ;;
        1) set_env WUI_LOG_FORMAT "text" && confirm_restart ;;
        2) set_env WUI_LOG_FORMAT "json" && confirm_restart ;;
        *)
            echo -e "${red}Invalid option.${plain}\n"
            set_log_format
            ;;
    esac
}

set_db_source() {
    echo
    echo -e "  A file path for SQLite, or a DSN for PostgreSQL."
    echo -e "  ${yellow}Pointing at a different database does not migrate anything.${plain}"
    echo && read -rp "Database source: " value
    if [[ -z "$value" ]]; then
        LOGD "Cancelled"
        settings_menu
        return
    fi
    if [[ "$value" == postgres://* || "$value" == postgresql://* ]]; then
        set_env WUI_DB_DRIVER "postgres"
    else
        set_env WUI_DB_DRIVER "sqlite"
    fi
    set_env WUI_DB_SOURCE "$value"
    confirm_restart
}

reset_settings() {
    echo
    echo -e "  ${yellow}This clears the environment file only.${plain}"
    echo -e "  ${green}Customers, interfaces, keys and usage are not touched.${plain}"
    confirm "Reset every setting to its default?" "n"
    if [[ $? != 0 ]]; then
        settings_menu
        return
    fi
    rm -f "$ENV_FILE"
    LOGI "Settings reset. The panel is back on its defaults."
    confirm_restart
}

# ── diagnostics ──────────────────────────────────────────────────────────────
#
# These answer the two questions an operator actually asks: is a limit really
# being enforced, and why is a customer not connecting.

enforcement_menu() {
    echo
    echo -e "${green}${bold}  Enforcement diagnostics${plain}"
    echo

    if have nft; then
        echo -e "  nftables:          ${green}$(nft --version 2> /dev/null | awk '{print $2}')${plain}"
    else
        echo -e "  nftables:          ${red}not installed — no limit can be applied${plain}"
    fi

    modprobe nft_quota 2> /dev/null
    if nft add table inet wui_probe 2> /dev/null && nft add quota inet wui_probe q '{ over 1000 bytes }' 2> /dev/null; then
        echo -e "  Kernel quota:      ${green}supported${plain}"
        echo -e "                     ${green}A customer is stopped at the byte they run out.${plain}"
    else
        echo -e "  Kernel quota:      ${red}not supported by this kernel${plain}"
        echo -e "                     ${yellow}Limits fall back to polling, so a customer on a fast${plain}"
        echo -e "                     ${yellow}link can overshoot before the panel notices.${plain}"
    fi
    nft delete table inet wui_probe 2> /dev/null

    local fwd4 fwd6
    fwd4=$(sysctl -n net.ipv4.ip_forward 2> /dev/null)
    fwd6=$(sysctl -n net.ipv6.conf.all.forwarding 2> /dev/null)
    [[ "$fwd4" == 1 ]] && echo -e "  IPv4 forwarding:   ${green}on${plain}" || echo -e "  IPv4 forwarding:   ${red}off — customers reach nothing${plain}"
    [[ "$fwd6" == 1 ]] && echo -e "  IPv6 forwarding:   ${green}on${plain}" || echo -e "  IPv6 forwarding:   ${yellow}off${plain}"

    if [[ -c /dev/net/tun ]]; then
        echo -e "  /dev/net/tun:      ${green}present${plain}"
    else
        echo -e "  /dev/net/tun:      ${red}missing — OpenVPN cannot start${plain}"
    fi

    # A speed limit needs a classful scheduler. Without one tc takes the command
    # and the kernel ignores it, so the panel would show a cap nothing enforces.
    modprobe sch_htb 2> /dev/null || true
    if ip link add wui-htbprobe type dummy 2> /dev/null; then
        if tc qdisc replace dev wui-htbprobe root handle 1: htb default ffff 2> /dev/null; then
            echo -e "  Speed limits:      ${green}enforced (HTB)${plain}"
        else
            echo -e "  Speed limits:      ${red}this kernel has no HTB scheduler${plain}"
            echo -e "                     ${yellow}Per-customer speed caps are recorded but never applied.${plain}"
        fi
        ip link del wui-htbprobe 2> /dev/null || true
    else
        echo -e "  Speed limits:      ${yellow}could not be checked${plain}"
    fi

    echo
    echo -e "${green}\t1.${plain} Show the live ruleset"
    echo -e "${green}\t2.${plain} Show per-customer counters"
    echo -e "${green}\t3.${plain} Show what the panel reports"
    echo -e "${green}\t4.${plain} Show the shaping hierarchy"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0) show_menu ;;
        1)
            nft list table inet wui 2>&1 | sed 's/^/  /'
            echo && read -rp "Press enter to continue: " temp
            enforcement_menu
            ;;
        2)
            echo
            nft -j list counters table inet wui 2> /dev/null \
                | grep -oE '"name":"[^"]+","handle":[0-9]+,"packets":[0-9]+,"bytes":[0-9]+' \
                | sed -E 's/"name":"([^"]+)".*"packets":([0-9]+),"bytes":([0-9]+)/  \1: \3 bytes over \2 packets/' \
                || echo "  (no counters yet)"
            echo && read -rp "Press enter to continue: " temp
            enforcement_menu
            ;;
        3)
            local port
            port=$(panel_port)
            curl -fsS "http://127.0.0.1:${port}/api/health" 2> /dev/null | sed 's/^/  /'
            echo
            journalctl -u "$SERVICE" --no-pager | grep -iE "enforcement|quota|rate limit" | tail -5 | sed 's/^/  /'
            echo && read -rp "Press enter to continue: " temp
            enforcement_menu
            ;;
        4)
            echo
            local shown=0 dev
            for dev in $(ip -o link show 2> /dev/null | awk -F': ' '{print $2}' | cut -d@ -f1); do
                if tc class show dev "$dev" 2> /dev/null | grep -q '^class htb'; then
                    echo -e "  ${green}${dev}${plain}"
                    tc -s class show dev "$dev" | grep -E '^class htb' | sed 's/^/    /'
                    shown=1
                fi
            done
            if [[ $shown == 0 ]]; then
                echo -e "  ${yellow}No shaping classes. Either no customer has a speed limit,${plain}"
                echo -e "  ${yellow}or this kernel cannot shape — see the summary above.${plain}"
            fi
            echo && read -rp "Press enter to continue: " temp
            enforcement_menu
            ;;
        4)
            echo
            local shown=0
            for dev in $(ip -o link show 2> /dev/null | awk -F": " "{print \$2}" | cut -d@ -f1); do
                if tc class show dev "$dev" 2> /dev/null | grep -q "^class htb"; then
                    echo -e "  ${green}${dev}${plain}"
                    tc -s class show dev "$dev" | grep -E "^class htb" | sed "s/^/    /"
                    shown=1
                fi
            done
            [[ $shown == 0 ]] && echo -e "  ${yellow}No shaping classes. Either no customer has a speed limit,${plain}"                 && echo -e "  ${yellow}or this kernel cannot shape — see the summary above.${plain}"
            echo && read -rp "Press enter to continue: " temp
            enforcement_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            enforcement_menu
            ;;
    esac
}

interface_overview() {
    echo
    echo -e "${green}${bold}  Interfaces${plain}"
    echo

    local found=0
    for dev in $(ip -o link show 2> /dev/null | awk -F': ' '{print $2}'); do
        local kind
        kind=$(ip -d link show "$dev" 2> /dev/null | grep -oE 'wireguard|amneziawg' | head -1)
        if [[ -n "$kind" ]]; then
            found=1
            local addr peers
            addr=$(ip -4 -brief addr show "$dev" | awk '{print $3}')
            peers=$(wg show "$dev" peers 2> /dev/null | wc -l)
            echo -e "  ${green}${dev}${plain}  ${kind}  ${addr}  ${peers} peer(s)"
        fi
    done

    for conf in "$DATA_DIR"/openvpn/*/server.conf; do
        [[ -f "$conf" ]] || continue
        found=1
        local name dir sessions state
        dir=$(dirname "$conf")
        name=$(basename "$dir")
        sessions=$(grep -c '^CLIENT_LIST' "$dir/status" 2> /dev/null || echo 0)
        if [[ -f "$dir/openvpn.pid" ]] && kill -0 "$(cat "$dir/openvpn.pid")" 2> /dev/null; then
            state="${green}running${plain}"
        else
            state="${red}stopped${plain}"
        fi
        echo -e "  ${green}${name}${plain}  openvpn  ${state}  ${sessions} session(s)"
    done

    [[ $found == 0 ]] && echo -e "  ${yellow}No interfaces yet. Create one in the panel.${plain}"

    echo
    echo -e "${green}── Listening ports ────────────────────${plain}"
    ss -lunp 2> /dev/null | grep -E 'wg|openvpn|:[0-9]+' | head -10 | sed 's/^/  /'

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

client_summary() {
    local info
    info=$(panel_cli setting show 2>&1)
    if [[ $? != 0 ]]; then
        LOGE "Could not read the database: $info"
        before_show_menu
        return
    fi

    echo
    echo -e "${green}${bold}  Customers${plain}"
    echo
    echo "$info" | grep -E '^(interfaces|clients|activeClients|accounts):' | sed 's/^/  /'
    echo
    echo -e "  ${yellow}Create, edit and share customer configs in the web panel.${plain}"

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

# ── firewall ─────────────────────────────────────────────────────────────────

install_firewall() {
    if have ufw; then
        LOGI "ufw is already installed"
        return 0
    fi
    LOGI "Installing ufw"
    if have apt-get; then
        apt-get update -qq && apt-get install -y -qq ufw
    elif have dnf; then
        dnf install -y ufw
    elif have yum; then
        yum install -y ufw
    else
        LOGE "No supported package manager found"
        return 1
    fi

    # Locking out the SSH session that is running this script is the single
    # most common way to lose a server, so the rules go in before it is enabled.
    ufw allow ssh > /dev/null 2>&1
    ufw allow "$(panel_port)" > /dev/null 2>&1
    ufw --force enable
    LOGI "ufw enabled with SSH and the panel port open"
}

open_ports() {
    echo
    echo -e "  Give ports or ranges separated by commas."
    echo -e "  ${yellow}Examples: 2096  ·  51820/udp  ·  1194/udp,2096${plain}"
    echo && read -rp "Ports: " ports
    if [[ -z "$ports" ]]; then
        LOGD "Cancelled"
        return
    fi
    IFS=',' read -ra list <<< "$ports"
    for p in "${list[@]}"; do
        p=$(echo "$p" | tr -d ' ')
        [[ -z "$p" ]] && continue
        if ufw allow "$p" > /dev/null 2>&1; then
            LOGI "Opened $p"
        else
            LOGE "Could not open $p"
        fi
    done
    ufw reload > /dev/null 2>&1
}

delete_ports() {
    ufw status numbered
    echo && read -rp "Rule number to delete: " num
    [[ -z "$num" ]] && return
    ufw --force delete "$num"
}

open_vpn_ports() {
    local opened=0
    for dev in $(ip -o link show 2> /dev/null | awk -F': ' '{print $2}'); do
        local port
        port=$(wg show "$dev" listen-port 2> /dev/null)
        if [[ -n "$port" ]]; then
            ufw allow "${port}/udp" > /dev/null 2>&1 && LOGI "Opened ${port}/udp for ${dev}" && opened=1
        fi
    done
    for conf in "$DATA_DIR"/openvpn/*/server.conf; do
        [[ -f "$conf" ]] || continue
        local port proto
        port=$(grep -E '^port ' "$conf" | awk '{print $2}')
        proto=$(grep -E '^proto ' "$conf" | awk '{print $2}')
        [[ -n "$port" ]] && ufw allow "${port}/${proto:-udp}" > /dev/null 2>&1 && LOGI "Opened ${port}/${proto} for $(basename "$(dirname "$conf")")" && opened=1
    done
    ufw allow "$(panel_port)" > /dev/null 2>&1 && LOGI "Opened $(panel_port) for the panel"
    [[ $opened == 0 ]] && LOGD "No tunnel ports found; create an interface first."
    ufw reload > /dev/null 2>&1
}

firewall_menu() {
    if ! have ufw; then
        echo -e "  ${yellow}ufw is not installed.${plain}"
    fi
    echo -e "${green}\t1.${plain} ${green}Install${plain} Firewall"
    echo -e "${green}\t2.${plain} Port List [numbered]"
    echo -e "${green}\t3.${plain} ${green}Open${plain} Ports"
    echo -e "${green}\t4.${plain} ${green}Open${plain} every tunnel port automatically"
    echo -e "${green}\t5.${plain} ${red}Delete${plain} Ports from List"
    echo -e "${green}\t6.${plain} ${green}Enable${plain} Firewall"
    echo -e "${green}\t7.${plain} ${red}Disable${plain} Firewall"
    echo -e "${green}\t8.${plain} Firewall Status"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice
    case "$choice" in
        0) show_menu ;;
        1) install_firewall && firewall_menu ;;
        2) ufw status numbered; firewall_menu ;;
        3) open_ports; firewall_menu ;;
        4) open_vpn_ports; firewall_menu ;;
        5) delete_ports; firewall_menu ;;
        6)
            ufw allow ssh > /dev/null 2>&1
            ufw --force enable
            firewall_menu
            ;;
        7) ufw disable; firewall_menu ;;
        8) ufw status verbose; firewall_menu ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            firewall_menu
            ;;
    esac
}

# ── TLS ──────────────────────────────────────────────────────────────────────

ssl_cert_issue() {
    echo
    echo -e "  ${yellow}The panel serves plain HTTP. A certificate here lets you put it${plain}"
    echo -e "  ${yellow}behind TLS, which matters because the admin password and every${plain}"
    echo -e "  ${yellow}customer's configuration travel over this connection.${plain}"
    echo
    echo -e "${green}\t1.${plain} Issue a certificate with acme.sh (needs a domain)"
    echo -e "${green}\t2.${plain} Show installed certificates"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0) show_menu ;;
        1) issue_acme_cert ;;
        2)
            if [[ -d /root/.acme.sh ]]; then
                ~/.acme.sh/acme.sh --list 2>&1 | sed 's/^/  /'
            else
                LOGD "acme.sh is not installed"
            fi
            echo && read -rp "Press enter to continue: " temp
            ssl_cert_issue
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            ssl_cert_issue
            ;;
    esac
}

issue_acme_cert() {
    if ! have socat; then
        LOGI "Installing socat"
        have apt-get && apt-get update -qq && apt-get install -y -qq socat
        have dnf && dnf install -y socat
    fi
    if [[ ! -d /root/.acme.sh ]]; then
        LOGI "Installing acme.sh"
        curl -fsSL https://get.acme.sh | sh
    fi

    echo && read -rp "Domain pointing at this server: " domain
    if [[ -z "$domain" ]]; then
        LOGD "Cancelled"
        ssl_cert_issue
        return
    fi

    # Port 80 has to be free for the HTTP-01 challenge, and it usually is not
    # obvious what is holding it.
    if ss -lnt 2> /dev/null | awk '$4 ~ /:80$/ {found=1} END {exit !found}'; then
        LOGE "Something is already listening on port 80; stop it and try again"
        ssl_cert_issue
        return
    fi
    have ufw && ufw allow 80 > /dev/null 2>&1

    local dir="$CONF_DIR/tls/$domain"
    mkdir -p "$dir"
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt > /dev/null 2>&1
    if ~/.acme.sh/acme.sh --issue -d "$domain" --standalone; then
        ~/.acme.sh/acme.sh --installcert -d "$domain" \
            --key-file "$dir/privkey.pem" \
            --fullchain-file "$dir/fullchain.pem"
        chmod 0640 "$dir"/*.pem
        chgrp "$SERVICE_USER" "$dir"/*.pem 2> /dev/null
        LOGI "Certificate installed in $dir"
        echo -e "  ${yellow}The panel does not terminate TLS itself. Point a reverse proxy${plain}"
        echo -e "  ${yellow}at http://127.0.0.1:$(panel_port) and give it these files.${plain}"
    else
        LOGE "Could not issue a certificate; check that the domain resolves here"
    fi
    echo && read -rp "Press enter to continue: " temp
    ssl_cert_issue
}

# ── backup ───────────────────────────────────────────────────────────────────

backup_menu() {
    echo
    echo -e "${green}${bold}  Backup and restore${plain}"
    echo -e "  ${yellow}The archive holds the database, every interface key and every${plain}"
    echo -e "  ${yellow}customer credential. Treat it like a password file.${plain}"
    echo
    echo -e "${green}\t1.${plain} Create a backup"
    echo -e "${green}\t2.${plain} Restore from a backup"
    echo -e "${green}\t3.${plain} List backups"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0) show_menu ;;
        1)
            local out="/root/wui-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
            if tar czf "$out" -C / "${DATA_DIR#/}" "${CONF_DIR#/}" 2> /dev/null; then
                chmod 0600 "$out"
                LOGI "Backup written to $out"
            else
                LOGE "Backup failed"
            fi
            echo && read -rp "Press enter to continue: " temp
            backup_menu
            ;;
        2)
            echo && read -rp "Path to the backup archive: " archive
            if [[ ! -f "$archive" ]]; then
                LOGE "No such file"
                backup_menu
                return
            fi
            confirm "Restoring replaces every customer and key currently on this server" "n"
            if [[ $? != 0 ]]; then
                backup_menu
                return
            fi
            systemctl stop "$SERVICE" 2> /dev/null
            if tar xzf "$archive" -C /; then
                chown -R "$SERVICE_USER":"$SERVICE_USER" "$DATA_DIR" 2> /dev/null
                LOGI "Restored"
                systemctl start "$SERVICE" 2> /dev/null
            else
                LOGE "Restore failed"
                systemctl start "$SERVICE" 2> /dev/null
            fi
            echo && read -rp "Press enter to continue: " temp
            backup_menu
            ;;
        3)
            ls -lh /root/wui-backup-*.tar.gz 2> /dev/null | sed 's/^/  /' || echo -e "  ${yellow}No backups yet${plain}"
            echo && read -rp "Press enter to continue: " temp
            backup_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            backup_menu
            ;;
    esac
}

# ── system ───────────────────────────────────────────────────────────────────

enable_bbr() {
    if grep -q "^net.core.default_qdisc=fq" /etc/sysctl.conf 2> /dev/null &&
        grep -q "^net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf 2> /dev/null; then
        LOGI "BBR is already enabled"
        before_show_menu
        return
    fi

    echo -e "  ${yellow}BBR usually improves throughput on long-distance links, which is${plain}"
    echo -e "  ${yellow}most of what a VPN customer does.${plain}"
    confirm "Enable BBR?" "y"
    if [[ $? != 0 ]]; then
        show_menu
        return
    fi

    sed -i '/^net.core.default_qdisc/d;/^net.ipv4.tcp_congestion_control/d' /etc/sysctl.conf
    echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
    echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
    sysctl -p > /dev/null 2>&1

    if [[ "$(sysctl -n net.ipv4.tcp_congestion_control 2> /dev/null)" == "bbr" ]]; then
        LOGI "BBR enabled"
    else
        LOGE "Could not enable BBR; this kernel may not support it"
    fi
    before_show_menu
}

run_speedtest() {
    if ! have speedtest; then
        LOGI "Installing speedtest"
        if have apt-get; then
            apt-get update -qq && apt-get install -y -qq speedtest-cli
        elif have dnf; then
            dnf install -y speedtest-cli
        else
            LOGE "No supported package manager found"
            before_show_menu
            return
        fi
    fi
    have speedtest && speedtest || speedtest-cli
    before_show_menu
}

# ── the menu ─────────────────────────────────────────────────────────────────

show_menu() {
    echo
    box_top
    box_title "W-UI · WireGuard & OpenVPN Panel"
    box_row_plain 0 "Exit Script"
    box_mid
    box_row "📦" 1 "Install"
    box_row "🔃" 2 "Update"
    box_row "📜" 3 "Update This Menu"
    box_row "🧹" 4 "Uninstall"
    box_mid
    box_row "🔑" 5 "Reset Admin Password"
    box_row "🔧" 6 "Settings"
    box_row "🔌" 7 "Change Panel Port"
    box_row "📋" 8 "View Current Settings"
    box_mid
    box_row "🟢" 9 "Start"
    box_row "🔴" 10 "Stop"
    box_row "🔄" 11 "Restart"
    box_row "📊" 12 "Check Status"
    box_row "📁" 13 "Logs Management"
    box_mid
    box_row "✅" 14 "Enable Autostart"
    box_row "❌" 15 "Disable Autostart"
    box_mid
    box_row "🧱" 16 "Enforcement Diagnostics"
    box_row "🌐" 17 "Interface Overview"
    box_row "👥" 18 "Customer Summary"
    box_row "🔥" 19 "Firewall Management"
    box_row "🔒" 20 "SSL Certificate Management"
    box_mid
    box_row "💾" 21 "Backup & Restore"
    box_row "🚀" 22 "Enable BBR"
    box_row "📡" 23 "Speedtest by Ookla"
    box_end
    echo

    show_status
    echo && read -rp "Please enter your selection [0-23]: " num

    case "${num}" in
        0) exit 0 ;;
        1) check_uninstall && install_panel ;;
        2) check_install && update ;;
        3) update_menu ;;
        4) check_install && uninstall ;;
        5) check_install && reset_admin ;;
        6) check_install && settings_menu ;;
        7) check_install && set_port ;;
        8) check_install && check_config ;;
        9) check_install && start ;;
        10) check_install && stop ;;
        11) check_install && restart ;;
        12) check_install && status ;;
        13) check_install && show_log ;;
        14) check_install && enable ;;
        15) check_install && disable ;;
        16) enforcement_menu ;;
        17) interface_overview ;;
        18) check_install && client_summary ;;
        19) firewall_menu ;;
        20) ssl_cert_issue ;;
        21) check_install && backup_menu ;;
        22) enable_bbr ;;
        23) run_speedtest ;;
        *) LOGE "Please enter the correct number [0-23]" ;;
    esac
}

show_usage() {
    echo -e "${green}W-UI management script${plain}"
    echo
    echo "  w-ui              open this menu"
    echo "  w-ui start        start the panel"
    echo "  w-ui stop         stop the panel"
    echo "  w-ui restart      restart the panel"
    echo "  w-ui status       show the service status"
    echo "  w-ui enable       start the panel at boot"
    echo "  w-ui disable      do not start the panel at boot"
    echo "  w-ui log          follow the log"
    echo "  w-ui settings     show the current settings"
    echo "  w-ui admin        reset the administrator account"
    echo "  w-ui check        run the enforcement diagnostics"
    echo "  w-ui install      install the panel"
    echo "  w-ui update       update the panel"
    echo "  w-ui uninstall    remove the panel"
}

if [[ $# > 0 ]]; then
    case $1 in
        "start") check_install 0 && start 0 ;;
        "stop") check_install 0 && stop 0 ;;
        "restart") check_install 0 && restart 0 ;;
        "status") check_install 0 && status 0 ;;
        "enable") check_install 0 && enable 0 ;;
        "disable") check_install 0 && disable 0 ;;
        "log") check_install 0 && journalctl -u "$SERVICE" -e --no-pager -f ;;
        "settings") check_install 0 && check_config 0 ;;
        "admin") check_install 0 && reset_admin 0 ;;
        "check") enforcement_menu ;;
        "interfaces") interface_overview 0 ;;
        "install") check_uninstall 0 && install_panel 0 ;;
        "update") check_install 0 && update 0 ;;
        "uninstall") check_install 0 && uninstall 0 ;;
        *) show_usage ;;
    esac
else
    show_menu
fi
