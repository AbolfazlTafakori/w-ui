#!/bin/bash

# W-UI management script.
#
# Everything an operator does to the panel from a terminal lives here, laid
# out the way 3x-ui's x-ui.sh is: the same menu, the same numbers, the same
# answers, for a panel that runs WireGuard and OpenVPN instead of Xray.
# Anything that needs the database goes through the panel binary rather than
# reading SQLite from a shell, so the schema has exactly one implementation.

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

#Add some basic function here
function LOGD() {
    echo -e "${yellow}[DEG] $* ${plain}"
}

function LOGE() {
    echo -e "${red}[ERR] $* ${plain}"
}

function LOGI() {
    echo -e "${green}[INF] $* ${plain}"
}

function LOGW() {
    echo -e "${yellow}[WRN] $* ${plain}"
}

# Port helpers: detect listener and owning process (best effort)
is_port_in_use() {
    local port="$1"
    if command -v ss > /dev/null 2>&1; then
        ss -ltn 2> /dev/null | awk -v p=":${port}$" '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v netstat > /dev/null 2>&1; then
        netstat -lnt 2> /dev/null | awk -v p=":${port} " '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v lsof > /dev/null 2>&1; then
        lsof -nP -iTCP:${port} -sTCP:LISTEN > /dev/null 2>&1 && return 0
    fi
    return 1
}

# Simple helpers for domain/IP validation
is_ipv4() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && return 0 || return 1
}
is_ipv6() {
    [[ "$1" =~ : ]] && return 0 || return 1
}
is_ip() {
    is_ipv4 "$1" || is_ipv6 "$1"
}
is_domain() {
    [[ "$1" =~ ^([A-Za-z0-9](-*[A-Za-z0-9])*\.)+(xn--[a-z0-9]{2,}|[A-Za-z]{2,})$ ]] && return 0 || return 1
}

# acme.sh's standalone server binds IPv4 by default; --listen-v6 makes it
# v6-only, which breaks HTTP-01 validation when the domain's A record points
# at this host's IPv4. Only force IPv6 when the host has no global IPv4
# address at all.
acme_listen_flag() {
    if ip -4 addr show scope global 2> /dev/null | grep -q "inet "; then
        echo ""
    else
        echo "--listen-v6"
    fi
}

# check root
[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \n" && exit 1

# Check OS and set release variable
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "Failed to check the system OS, please contact the author!" >&2
    exit 1
fi
echo "The OS release is: $release"

os_version=""
os_version=$(grep "^VERSION_ID" /etc/os-release | cut -d '=' -f2 | tr -d '"' | tr -d '.')

# Declare Variables
BIN_PATH=/usr/local/bin/wui
DATA_DIR=/var/lib/wui
CONF_DIR=/etc/wui
ENV_FILE=$CONF_DIR/wui.env
CERT_ROOT=$CONF_DIR/certs
SERVICE=wui
SERVICE_USER=wui
REPO=AbolfazlTafakori/w-ui
REPO_RAW=https://raw.githubusercontent.com/${REPO}/main
log_folder="${WUI_LOG_FOLDER:=/var/log/wui}"
mkdir -p "${log_folder}"
iplimit_banned_log_path="${log_folder}/wui-banned.log"

# acme.sh lives under root's home regardless of who ran this: its renewal
# runs as root, and a script run under `sudo` often carries the calling
# user's HOME.
ACME_HOME="$( { getent passwd root 2>/dev/null || true; } | cut -d: -f6)"
ACME_HOME="${ACME_HOME:-/root}/.acme.sh"
acme() { "$ACME_HOME/acme.sh" --home "$ACME_HOME" "$@"; }

have() { command -v "$1" > /dev/null 2>&1; }

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
    confirm "Restart the panel, Attention: Restarting the panel does not disconnect customers" "y"
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

# panel_cli runs the binary with the same configuration the service uses, so
# what it prints is what the panel actually sees.
panel_cli() {
    (
        set -a
        WUI_DATA_DIR="$DATA_DIR"
        WUI_DB_SOURCE="$DATA_DIR/wui.db"
        for kv in $(grep -oE 'WUI_[A-Z_]+=[^ ]+' /etc/systemd/system/${SERVICE}.service 2> /dev/null); do
            export "$kv"
        done
        [[ -f "$ENV_FILE" ]] && . "$ENV_FILE"
        set +a
        "$BIN_PATH" "$@"
    )
}

# What the panel answers on, as the panel itself sees it.
setting_value() {
    panel_cli setting show 2> /dev/null | grep -E "^$1: " | head -1 | cut -d' ' -f2-
}

install() {
    bash <(curl -Ls "${REPO_RAW}/install.sh")
    if [[ $? == 0 ]]; then
        if [[ $# == 0 ]]; then
            start
        else
            start 0
        fi
    fi
}

update() {
    confirm "This function will update all W-UI components to the latest version, and the data will not be lost. Do you want to continue?" "y"
    if [[ $? != 0 ]]; then
        LOGE "Cancelled"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    bash <(curl -Ls "${REPO_RAW}/install.sh")
    if [[ $? == 0 ]]; then
        LOGI "Update is complete, Panel has automatically restarted "
        before_show_menu
    fi
}

update_dev() {
    confirm "This will update W-UI to the latest commit on main (built from source, not a stable release). Your data is preserved. Continue?" "y"
    if [[ $? != 0 ]]; then
        LOGE "Cancelled"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    bash <(curl -Ls "${REPO_RAW}/install.sh") --from-source
    if [[ $? == 0 ]]; then
        LOGI "Dev update is complete, Panel has automatically restarted "
        before_show_menu
    fi
}

replace_wui_script() {
    local url="$1"
    local use_if_modified_since="$2"
    local temp_file="/usr/local/bin/w-ui-temp.$$"

    rm -f "$temp_file"
    if [[ "$use_if_modified_since" == "true" ]]; then
        curl -fLRo "$temp_file" -z /usr/local/bin/w-ui "$url"
    else
        curl -fLRo "$temp_file" "$url"
    fi
    if [[ $? != 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi

    if [[ ! -s "$temp_file" ]]; then
        rm -f "$temp_file"
        # -z above means "not modified since /usr/local/bin/w-ui" rather than a
        # real failure, so an empty download here is success, not an error.
        [[ "$use_if_modified_since" == "true" ]] && return 0
        return 1
    fi

    mv -f "$temp_file" /usr/local/bin/w-ui
    if [[ $? != 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    chmod +x /usr/local/bin/w-ui
    return 0
}

# The menu must match the installed panel, so update it from that release's
# tag; fall back to main only when no script is published for the version.
installed_script_url() {
    local ver
    ver=$("$BIN_PATH" version 2> /dev/null | tr -d '[:space:]')
    ver="${ver#v}"
    if [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && curl -fsIL -o /dev/null "https://raw.githubusercontent.com/${REPO}/v${ver}/w-ui.sh"; then
        echo "https://raw.githubusercontent.com/${REPO}/v${ver}/w-ui.sh"
    else
        echo -e "${yellow}No w-ui.sh published for the installed version (${ver:-unknown}), using main${plain}" >&2
        echo "${REPO_RAW}/w-ui.sh"
    fi
}

update_menu() {
    echo -e "${yellow}Updating Menu${plain}"
    confirm "This function will update the menu to the latest changes." "y"
    if [[ $? != 0 ]]; then
        LOGE "Cancelled"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi

    if replace_wui_script "$(installed_script_url)" "false"; then
        echo -e "${green}Update successful. The menu has been replaced; run w-ui again.${plain}"
        exit 0
    else
        echo -e "${red}Failed to update the menu.${plain}"
        return 1
    fi
}

legacy_version() {
    echo -n "Enter the panel version (like 1.4.0):"
    read -r tag_version

    if [ -z "$tag_version" ]; then
        echo "Panel version cannot be empty. Exiting."
        exit 1
    fi
    tag_version="${tag_version#v}"
    local arch
    case "$(uname -m)" in
        x86_64 | amd64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *) arch="$(uname -m)" ;;
    esac
    # Use the entered panel version in the download link
    install_command="WUI_RELEASE_URL=https://github.com/${REPO}/releases/download/v${tag_version}/wui-linux-${arch} bash <(curl -Ls \"https://raw.githubusercontent.com/${REPO}/v${tag_version}/install.sh\")"

    echo "Downloading and installing panel version $tag_version..."
    eval $install_command
}

# Function to handle the deletion of the script file
delete_script() {
    rm "$0" # Remove the script file itself
    exit 1
}

uninstall() {
    confirm "Are you sure you want to uninstall the panel? Every tunnel it runs will also be removed!" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi

    systemctl stop "$SERVICE"
    systemctl disable "$SERVICE"
    systemctl disable --now wui-cert-renew.timer 2> /dev/null
    rm -f /etc/systemd/system/${SERVICE}.service /etc/systemd/system/wui-cert-renew.service /etc/systemd/system/wui-cert-renew.timer
    systemctl daemon-reload
    systemctl reset-failed

    rm "$CONF_DIR"/ -rf
    rm "$DATA_DIR"/ -rf
    rm -f "$BIN_PATH"

    echo ""
    echo -e "Uninstalled Successfully.\n"
    echo "If you need to install this panel again, you can use below command:"
    echo -e "${green}bash <(curl -Ls ${REPO_RAW}/install.sh)${plain}"
    echo ""
    # Trap the SIGTERM signal
    trap delete_script SIGTERM
    delete_script
}

reset_user() {
    confirm "Are you sure to reset the username and password of the panel?" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi

    read -rp "Please set the login username [default is a random username]: " config_account
    [[ -z $config_account ]] && config_account=$(gen_random_string 10)
    read -rp "Please set the login password [default is a random password]: " config_password
    [[ -z $config_password ]] && config_password=$(gen_random_string 18)

    panel_cli admin reset --username "${config_account}" --password "${config_password}" --quiet > /dev/null 2>&1

    echo -e "Panel login username has been reset to: ${green} ${config_account} ${plain}"
    echo -e "Panel login password has been reset to: ${green} ${config_password} ${plain}"
    echo -e "${green} Please use the new login username and password to access the W-UI panel. Also remember them! ${plain}"
    confirm_restart
}

gen_random_string() {
    local length="$1"
    openssl rand -base64 $((length * 2)) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

reset_webbasepath() {
    echo -e "${yellow}Resetting Web Base Path${plain}"

    read -rp "Are you sure you want to reset the web base path? (y/n): " confirm
    if [[ $confirm != "y" && $confirm != "Y" ]]; then
        echo -e "${yellow}Operation canceled.${plain}"
        return
    fi

    config_webBasePath=$(gen_random_string 18)

    # Apply the new web base path setting
    panel_cli setting set --base-path "/${config_webBasePath}/" > /dev/null 2>&1

    echo -e "Web base path has been reset to: ${green}${config_webBasePath}${plain}"
    echo -e "${green}Please use the new web base path to access the panel.${plain}"
    restart
}

reset_config() {
    confirm "Are you sure you want to reset all panel settings, Account data will not be lost, Username and password will not change" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    panel_cli setting reset
    echo -e "All panel settings have been reset to default."
    restart
}

# This server's address as the internet sees it, from the first echo service
# that answers.
detect_server_ip() {
    local URL_lists=(
        "https://api4.ipify.org"
        "https://ipv4.icanhazip.com"
        "https://v4.api.ipinfo.io/ip"
        "https://ipv4.myexternalip.com/raw"
        "https://4.ident.me"
        "https://check-host.net/ip"
    )
    local ip_address
    for ip_address in "${URL_lists[@]}"; do
        local response=$(curl -s -w "\n%{http_code}" --max-time 3 "${ip_address}" 2> /dev/null)
        local http_code=$(echo "$response" | tail -n1)
        local ip_result=$(echo "$response" | head -n-1 | tr -d '[:space:]"')
        if [[ "${http_code}" == "200" && "${ip_result}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "${ip_result}"
            return 0
        fi
    done
    return 1
}

ask_server_ip() {
    local server_ip=""
    while [[ -z "$server_ip" ]]; do
        read -rp "Please enter your server's public IPv4 address: " server_ip
        server_ip="${server_ip// /}"
        if [[ ! "$server_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo -e "${red}Invalid IPv4 address. Please try again.${plain}"
            server_ip=""
        fi
    done
    echo "$server_ip"
}

check_config() {
    local info=$(panel_cli setting show)
    if [[ $? != 0 ]]; then
        LOGE "get current settings error, please check logs"
        show_menu
        return
    fi
    LOGI "${info}"
    echo -e "${green}Database: SQLite (${DATA_DIR}/wui.db)${plain}"

    local existing_webBasePath=$(echo "$info" | grep -Eo 'basePath: .+' | awk '{print $2}')
    local existing_port=$(echo "$info" | grep -Eo 'port: .+' | awk '{print $2}')
    local existing_cert=$(echo "$info" | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    local server_ip
    server_ip=$(detect_server_ip)

    if [[ -z "$server_ip" ]]; then
        echo -e "${yellow}Could not auto-detect server IP from any provider.${plain}"
        server_ip=$(ask_server_ip)
    fi

    if [[ -n "$existing_cert" ]]; then
        local domain=$(basename "$(dirname "$existing_cert")")
        # The cert folder name is only the certificate's first domain. A
        # multidomain (SAN) certificate may be served under any name it covers,
        # so read the real names from the certificate itself.
        local cert_sans=""
        if [[ -f "$existing_cert" ]] && command -v openssl > /dev/null 2>&1; then
            cert_sans=$(openssl x509 -in "$existing_cert" -noout -ext subjectAltName 2> /dev/null \
                | grep -Eo 'DNS:[^,[:space:]]+' | cut -d: -f2)
            if [[ -n "$cert_sans" ]] && ! echo "$cert_sans" | grep -qx "$domain"; then
                domain=$(echo "$cert_sans" | head -n1)
            fi
        fi

        if [[ "$domain" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${green}Access URL: https://${domain}:${existing_port}${existing_webBasePath}${plain}"
        else
            echo -e "${green}Access URL: https://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
        fi
        if [[ -n "$cert_sans" && $(echo "$cert_sans" | wc -l) -gt 1 ]]; then
            echo -e "${yellow}The certificate also covers:${plain} $(echo "$cert_sans" | grep -vx "$domain" | tr '\n' ' ')"
        fi
    else
        echo -e "${red}⚠ WARNING: No SSL certificate configured!${plain}"
        echo -e "${yellow}You can get a Let's Encrypt certificate for your IP address (valid ~6 days, auto-renews).${plain}"
        read -rp "Generate SSL certificate for IP now? [y/N]: " gen_ssl
        if [[ "$gen_ssl" == "y" || "$gen_ssl" == "Y" ]]; then
            stop 0 > /dev/null 2>&1
            ssl_cert_issue_for_ip
            if [[ $? -eq 0 ]]; then
                echo -e "${green}Access URL: https://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
                # ssl_cert_issue_for_ip already restarts the panel, but ensure it's running
                start 0 > /dev/null 2>&1
            else
                LOGE "IP certificate setup failed."
                echo -e "${yellow}You can try again via main menu option 20 (SSL Certificate Management).${plain}"
                start 0 > /dev/null 2>&1
            fi
        else
            echo -e "${yellow}Access URL: http://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
            echo -e "${yellow}For security, please configure SSL certificate using main menu option 20 (SSL Certificate Management)${plain}"
        fi
    fi
}

set_port() {
    echo -n "Enter port number[1-65535]: "
    read -r port
    if [[ -z "${port}" ]]; then
        LOGD "Cancelled"
        before_show_menu
    else
        panel_cli setting set --port ${port}
        echo -e "The port is set, Please restart the panel now, and use the new port ${green}${port}${plain} to access web panel"
        confirm_restart
    fi
}

start() {
    check_status
    if [[ $? == 0 ]]; then
        echo ""
        LOGI "Panel is running, No need to start again, If you need to restart, please select restart"
    else
        systemctl start "$SERVICE"
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            LOGI "w-ui Started Successfully"
        else
            LOGE "panel Failed to start, Probably because it takes longer than two seconds to start, Please check the log information later"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    check_status
    if [[ $? == 1 ]]; then
        echo ""
        LOGI "Panel stopped, No need to stop again!"
    else
        systemctl stop "$SERVICE"
        sleep 2
        check_status
        if [[ $? == 1 ]]; then
            LOGI "w-ui stopped successfully; the tunnels keep serving customers"
        else
            LOGE "Panel stop failed, Probably because the stop time exceeds two seconds, Please check the log information later"
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
        LOGI "w-ui Restarted successfully"
    else
        LOGE "Panel restart failed, Probably because it takes longer than two seconds to start, Please check the log information later"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

# Every tunnel brought up again from its stored configuration, without the
# panel itself going down: what "restart xray" is on 3x-ui.
restart_tunnels() {
    systemctl reload "$SERVICE"
    LOGI "Tunnel restart signal sent successfully, Please check the log information to confirm whether the tunnels restarted successfully"
    sleep 2
    show_tunnel_status
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    systemctl status "$SERVICE" -l
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    systemctl enable "$SERVICE"
    if [[ $? == 0 ]]; then
        LOGI "w-ui Set to boot automatically on startup successfully"
    else
        LOGE "w-ui Failed to set Autostart"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    systemctl disable "$SERVICE"
    if [[ $? == 0 ]]; then
        LOGI "w-ui Autostart Cancelled successfully"
    else
        LOGE "w-ui Failed to cancel autostart"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    echo -e "${green}\t1.${plain} Debug Log"
    echo -e "${green}\t2.${plain} Clear All logs"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0)
            show_menu
            ;;
        1)
            journalctl -u "$SERVICE" -e --no-pager -f -p debug
            if [[ $# == 0 ]]; then
                before_show_menu
            fi
            ;;
        2)
            sudo journalctl --rotate
            sudo journalctl --vacuum-time=1s
            echo "All Logs cleared."
            restart
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            show_log
            ;;
    esac
}

bbr_menu() {
    echo -e "${green}\t1.${plain} Enable BBR"
    echo -e "${green}\t2.${plain} Disable BBR"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            enable_bbr
            bbr_menu
            ;;
        2)
            disable_bbr
            bbr_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            bbr_menu
            ;;
    esac
}

disable_bbr() {

    if [[ $(sysctl -n net.ipv4.tcp_congestion_control) != "bbr" ]] || [[ ! $(sysctl -n net.core.default_qdisc) =~ ^(fq|cake)$ ]]; then
        echo -e "${yellow}BBR is not currently enabled.${plain}"
        before_show_menu
    fi

    if [ -f "/etc/sysctl.d/99-bbr-w-ui.conf" ]; then
        old_settings=$(head -1 /etc/sysctl.d/99-bbr-w-ui.conf | tr -d '#')
        # sysctl -w already restores the live values, so no `sysctl --system`
        # afterwards — it would re-apply every sysctl file on the host and
        # surface unrelated errors from the distro's own defaults
        sysctl -w net.core.default_qdisc="${old_settings%:*}"
        sysctl -w net.ipv4.tcp_congestion_control="${old_settings#*:}"
        rm /etc/sysctl.d/99-bbr-w-ui.conf
    else
        # Replace BBR with CUBIC configurations
        if [ -f "/etc/sysctl.conf" ]; then
            sed -i 's/net.core.default_qdisc=fq/net.core.default_qdisc=pfifo_fast/' /etc/sysctl.conf
            sed -i 's/net.ipv4.tcp_congestion_control=bbr/net.ipv4.tcp_congestion_control=cubic/' /etc/sysctl.conf
            sysctl -p
        fi
    fi

    if [[ $(sysctl -n net.ipv4.tcp_congestion_control) != "bbr" ]]; then
        echo -e "${green}BBR has been replaced with CUBIC successfully.${plain}"
    else
        echo -e "${red}Failed to replace BBR with CUBIC. Please check your system configuration.${plain}"
    fi
}

enable_bbr() {
    if [[ $(sysctl -n net.ipv4.tcp_congestion_control) == "bbr" ]] && [[ $(sysctl -n net.core.default_qdisc) =~ ^(fq|cake)$ ]]; then
        echo -e "${green}BBR is already enabled!${plain}"
        before_show_menu
    fi

    # Enable BBR
    if [ -d "/etc/sysctl.d/" ]; then
        {
            echo "#$(sysctl -n net.core.default_qdisc):$(sysctl -n net.ipv4.tcp_congestion_control)"
            echo "net.core.default_qdisc = fq"
            echo "net.ipv4.tcp_congestion_control = bbr"
        } > "/etc/sysctl.d/99-bbr-w-ui.conf"
        if [ -f "/etc/sysctl.conf" ]; then
            # Backup old settings from sysctl.conf, if any
            sed -i 's/^net.core.default_qdisc/# &/' /etc/sysctl.conf
            sed -i 's/^net.ipv4.tcp_congestion_control/# &/' /etc/sysctl.conf
        fi
        # Apply only our config file; `sysctl --system` would re-apply every
        # sysctl file on the host and surface unrelated errors from the distro's
        # own defaults
        sysctl -p /etc/sysctl.d/99-bbr-w-ui.conf
    else
        sed -i '/net.core.default_qdisc/d' /etc/sysctl.conf
        sed -i '/net.ipv4.tcp_congestion_control/d' /etc/sysctl.conf
        echo "net.core.default_qdisc=fq" | tee -a /etc/sysctl.conf
        echo "net.ipv4.tcp_congestion_control=bbr" | tee -a /etc/sysctl.conf
        sysctl -p
    fi

    # Verify that BBR is enabled
    if [[ $(sysctl -n net.ipv4.tcp_congestion_control) == "bbr" ]]; then
        echo -e "${green}BBR has been enabled successfully.${plain}"
    else
        echo -e "${red}Failed to enable BBR. Please check your system configuration.${plain}"
    fi
}

update_shell() {
    if replace_wui_script "$(installed_script_url)" "true"; then
        LOGI "Upgrade script succeeded, Please rerun the script"
        before_show_menu
    else
        echo ""
        LOGE "Failed to download script, Please check whether the machine can connect Github"
        before_show_menu
    fi
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ ! -f /etc/systemd/system/${SERVICE}.service ]]; then
        return 2
    fi
    temp=$(systemctl status "$SERVICE" | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
    if [[ "${temp}" == "running" ]]; then
        return 0
    else
        return 1
    fi
}

check_enabled() {
    temp=$(systemctl is-enabled "$SERVICE")
    if [[ "${temp}" == "enabled" ]]; then
        return 0
    else
        return 1
    fi
}

check_uninstall() {
    check_status
    if [[ $? != 2 ]]; then
        echo ""
        LOGE "Panel installed, Please do not reinstall"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

check_install() {
    check_status
    if [[ $? == 2 ]]; then
        echo ""
        LOGE "Please install the panel first"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
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
            ;;
    esac
    show_tunnel_status
}

show_enable_status() {
    check_enabled
    if [[ $? == 0 ]]; then
        echo -e "Start automatically: ${green}Yes${plain}"
    else
        echo -e "Start automatically: ${red}No${plain}"
    fi
}

# show_tunnel_status is what "xray state" is on 3x-ui: one line per tunnel
# the panel runs, WireGuard and OpenVPN alike.
show_tunnel_status() {
    local any=0 dev seen=" "
    if have wg; then
        for dev in $(wg show interfaces 2> /dev/null); do
            any=1; seen+="$dev "
            echo -e "WireGuard ${dev} (udp/$(wg show "$dev" listen-port 2> /dev/null)): ${green}Running${plain}"
        done
    fi
    # AmneziaWG tunnels answer to awg, not wg.
    if have awg; then
        for dev in $(awg show interfaces 2> /dev/null); do
            [[ "$seen" == *" $dev "* ]] && continue
            any=1
            echo -e "AmneziaWG ${dev} (udp/$(awg show "$dev" listen-port 2> /dev/null)): ${green}Running${plain}"
        done
    fi
    local conf
    for conf in "$DATA_DIR"/openvpn/*/server.conf; do
        [[ -f "$conf" ]] || continue
        any=1
        local name port proto
        name=$(basename "$(dirname "$conf")")
        port=$(grep -E '^port ' "$conf" | awk '{print $2}')
        proto=$(grep -E '^proto ' "$conf" | awk '{print $2}')
        if pgrep -f "openvpn.*${name}" > /dev/null 2>&1 || ip link show "$name" > /dev/null 2>&1; then
            echo -e "OpenVPN ${name} (${proto:-udp}/${port}): ${green}Running${plain}"
        else
            echo -e "OpenVPN ${name} (${proto:-udp}/${port}): ${red}Not Running${plain}"
        fi
    done
    if [[ $any == 0 ]]; then
        echo -e "tunnel state: ${red}Not Running${plain}"
    fi
}

firewall_menu() {
    echo -e "${green}\t1.${plain} ${green}Install${plain} Firewall"
    echo -e "${green}\t2.${plain} Port List [numbered]"
    echo -e "${green}\t3.${plain} ${green}Open${plain} Ports"
    echo -e "${green}\t4.${plain} ${red}Delete${plain} Ports from List"
    echo -e "${green}\t5.${plain} ${green}Enable${plain} Firewall"
    echo -e "${green}\t6.${plain} ${red}Disable${plain} Firewall"
    echo -e "${green}\t7.${plain} Firewall Status"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            install_firewall
            firewall_menu
            ;;
        2)
            ufw status numbered
            firewall_menu
            ;;
        3)
            open_ports
            firewall_menu
            ;;
        4)
            delete_ports
            firewall_menu
            ;;
        5)
            ufw enable
            firewall_menu
            ;;
        6)
            ufw disable
            firewall_menu
            ;;
        7)
            ufw status verbose
            firewall_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            firewall_menu
            ;;
    esac
}

install_firewall() {
    if ! command -v ufw &> /dev/null; then
        echo "ufw firewall is not installed. Installing now..."
        apt-get update
        apt-get install -y ufw
    else
        echo "ufw firewall is already installed"
    fi

    # Check if the firewall is inactive
    if ufw status | grep -q "Status: active"; then
        echo "Firewall is already active"
    else
        echo "Activating firewall..."
        # Open the necessary ports
        ufw allow ssh
        ufw allow http
        ufw allow https
        local panel_port
        panel_port=$(setting_value port)
        [[ -n "$panel_port" ]] && ufw allow "${panel_port}/tcp" #webPort
        # Every tunnel the panel runs, so enabling the firewall does not
        # disconnect the customers on it.
        local dev conf
        for dev in $(wg show interfaces 2> /dev/null); do
            ufw allow "$(wg show "$dev" listen-port 2> /dev/null)/udp"
        done
        for dev in $(awg show interfaces 2> /dev/null); do
            ufw allow "$(awg show "$dev" listen-port 2> /dev/null)/udp"
        done
        for conf in "$DATA_DIR"/openvpn/*/server.conf; do
            [[ -f "$conf" ]] || continue
            ufw allow "$(grep -E '^port ' "$conf" | awk '{print $2}')/$(grep -E '^proto ' "$conf" | awk '{print $2}')"
        done

        # Enable the firewall
        ufw --force enable
    fi
}

open_ports() {
    # Prompt the user to enter the ports they want to open
    read -rp "Enter the ports you want to open (e.g. 80,443,2053 or range 400-500): " ports

    # Check if the input is valid
    if ! [[ $ports =~ ^([0-9]+|[0-9]+-[0-9]+)(,([0-9]+|[0-9]+-[0-9]+))*$ ]]; then
        echo "Error: Invalid input. Please enter a comma-separated list of ports or a range of ports (e.g. 80,443,2053 or 400-500)." >&2
        exit 1
    fi

    # Open the specified ports using ufw
    IFS=',' read -ra PORT_LIST <<< "$ports"
    for port in "${PORT_LIST[@]}"; do
        if [[ $port == *-* ]]; then
            # Split the range into start and end ports
            start_port=$(echo $port | cut -d'-' -f1)
            end_port=$(echo $port | cut -d'-' -f2)
            # Open the port range
            ufw allow $start_port:$end_port/tcp
            ufw allow $start_port:$end_port/udp
        else
            # Open the single port
            ufw allow "$port"
        fi
    done

    # Confirm that the ports are opened
    echo "Opened the specified ports:"
    for port in "${PORT_LIST[@]}"; do
        if [[ $port == *-* ]]; then
            start_port=$(echo $port | cut -d'-' -f1)
            end_port=$(echo $port | cut -d'-' -f2)
            # Check if the port range has been successfully opened
            (ufw status | grep -q "$start_port:$end_port") && echo "$start_port-$end_port"
        else
            # Check if the individual port has been successfully opened
            (ufw status | grep -q "$port") && echo "$port"
        fi
    done
}

delete_ports() {
    # Display current rules with numbers
    echo "Current UFW rules:"
    ufw status numbered

    # Ask the user how they want to delete rules
    echo "Do you want to delete rules by:"
    echo "1) Rule numbers"
    echo "2) Ports"
    read -rp "Enter your choice (1 or 2): " choice

    if [[ $choice -eq 1 ]]; then
        # Deleting by rule numbers
        read -rp "Enter the rule numbers you want to delete (1, 2, etc.): " rule_numbers

        # Validate the input
        if ! [[ $rule_numbers =~ ^([0-9]+)(,[0-9]+)*$ ]]; then
            echo "Error: Invalid input. Please enter a comma-separated list of rule numbers." >&2
            exit 1
        fi

        # Split numbers into an array
        IFS=',' read -ra RULE_NUMBERS <<< "$rule_numbers"
        for rule_number in "${RULE_NUMBERS[@]}"; do
            # Delete the rule by number
            ufw delete "$rule_number" || echo "Failed to delete rule number $rule_number"
        done

        echo "Selected rules have been deleted."

    elif [[ $choice -eq 2 ]]; then
        # Deleting by ports
        read -rp "Enter the ports you want to delete (e.g. 80,443,2053 or range 400-500): " ports

        # Validate the input
        if ! [[ $ports =~ ^([0-9]+|[0-9]+-[0-9]+)(,([0-9]+|[0-9]+-[0-9]+))*$ ]]; then
            echo "Error: Invalid input. Please enter a comma-separated list of ports or a range of ports (e.g. 80,443,2053 or 400-500)." >&2
            exit 1
        fi

        # Split ports into an array
        IFS=',' read -ra PORT_LIST <<< "$ports"
        for port in "${PORT_LIST[@]}"; do
            if [[ $port == *-* ]]; then
                # Split the port range
                start_port=$(echo $port | cut -d'-' -f1)
                end_port=$(echo $port | cut -d'-' -f2)
                # Delete the port range
                ufw delete allow $start_port:$end_port/tcp
                ufw delete allow $start_port:$end_port/udp
            else
                # Delete a single port
                ufw delete allow "$port"
            fi
        done

        # Confirmation of deletion
        echo "Deleted the specified ports:"
        for port in "${PORT_LIST[@]}"; do
            if [[ $port == *-* ]]; then
                start_port=$(echo $port | cut -d'-' -f1)
                end_port=$(echo $port | cut -d'-' -f2)
                # Check if the port range has been deleted
                (ufw status | grep -q "$start_port:$end_port") || echo "$start_port-$end_port"
            else
                # Check if the individual port has been deleted
                (ufw status | grep -q "$port") || echo "$port"
            fi
        done
    else
        echo "${red}Error:${plain} Invalid choice. Please enter 1 or 2." >&2
        exit 1
    fi
}

# The country lists the routing rules use ("geoip:ir"), kept as one file per
# country under the data directory and fetched from ipverse, which is where
# the panel itself fetches them.
update_geofiles() {
    local geo_dir="$DATA_DIR/geoip"
    local failed=0 f cc http_code
    local any=0
    for f in "$geo_dir"/*.txt; do
        [[ -f "$f" ]] || continue
        any=1
        cc=$(basename "$f" .txt)
        local temp_file="${f}.tmp.$$"
        rm -f "$temp_file"
        http_code=$(curl -sSfLo "$temp_file" -w '%{http_code}' \
            "https://raw.githubusercontent.com/ipverse/country-ip-blocks/master/country/${cc}/ipv4-aggregated.txt")
        if [[ $? -ne 0 ]]; then
            echo -e "${red}${cc}: download failed${plain}"
            rm -f "$temp_file"
            failed=1
            continue
        fi
        curl -sSfLo - "https://raw.githubusercontent.com/ipverse/country-ip-blocks/master/country/${cc}/ipv6-aggregated.txt" >> "$temp_file" 2> /dev/null
        grep -v '^#' "$temp_file" | grep -v '^$' > "${temp_file}.clean" && mv -f "${temp_file}.clean" "$temp_file"
        if [[ ! -s "$temp_file" ]]; then
            echo -e "${red}${cc}: downloaded file is empty${plain}"
            rm -f "$temp_file"
            failed=1
        elif cmp -s "$temp_file" "$f"; then
            echo -e "${cc}: already up to date"
            rm -f "$temp_file"
        else
            mv -f "$temp_file" "$f" && chown "$SERVICE_USER:$SERVICE_USER" "$f" 2> /dev/null
            echo -e "${green}${cc}: updated${plain}"
            geo_updated=1
        fi
    done
    if [[ $any == 0 ]]; then
        echo -e "${yellow}No country lists are in use yet; a rule with \"geoip:xx\" fetches one.${plain}"
    fi
    return $failed
}

run_geo_update() {
    local name="$1"
    shift
    geo_updated=0
    "$@"
    if [[ $? -ne 0 ]]; then
        echo -e "${red}Some ${name} could not be updated. Check the errors above.${plain}"
    elif [[ $geo_updated -eq 1 ]]; then
        echo -e "${green}${name} have been updated successfully!${plain}"
        restart
    else
        echo -e "${green}${name} are already up to date, restart is not needed.${plain}"
    fi
}

update_geo() {
    echo -e "${green}\t1.${plain} ipverse country lists (every country in use)"
    echo -e "${green}\t2.${plain} Fetch one country now (e.g. ir, ru, cn)"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice

    case "$choice" in
        0)
            show_menu
            ;;
        1)
            run_geo_update "geo files" update_geofiles
            ;;
        2)
            read -rp "Country code: " cc
            cc=$(echo "$cc" | tr 'A-Z' 'a-z' | tr -d '[:space:]')
            if [[ ! "$cc" =~ ^[a-z]{2}$ ]]; then
                echo -e "${red}A country is two letters, like ir.${plain}"
            else
                mkdir -p "$DATA_DIR/geoip"
                touch "$DATA_DIR/geoip/${cc}.txt"
                run_geo_update "geo files" update_geofiles
            fi
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            update_geo
            ;;
    esac

    before_show_menu
}

install_acme() {
    # Check if acme.sh is already installed
    if [[ -x "$ACME_HOME/acme.sh" ]]; then
        LOGI "acme.sh is already installed."
        return 0
    fi

    LOGI "Installing acme.sh..."
    cd ~ || return 1 # Ensure you can change to the home directory

    curl -fsSL https://get.acme.sh -o /tmp/get-acme.sh && (HOME="${ACME_HOME%/.acme.sh}" sh /tmp/get-acme.sh --home "$ACME_HOME")
    local rc=$?
    rm -f /tmp/get-acme.sh
    if [ $rc -ne 0 ] || [[ ! -x "$ACME_HOME/acme.sh" ]]; then
        LOGE "Installation of acme.sh failed."
        return 1
    else
        LOGI "Installation of acme.sh succeeded."
    fi

    ensure_renewal
    return 0
}

# Renewal must happen with nobody watching. acme.sh renews from a cron
# entry -- which is nothing on the many small images that ship without a
# cron daemon -- so a systemd timer runs its check every six hours as well.
# Six hours, not a day: an IP certificate lives six days and is renewed at
# six, so a daily check could miss the window.
ensure_renewal() {
    acme --install-cronjob > /dev/null 2>&1 || true
    cat > /etc/systemd/system/wui-cert-renew.service <<UNIT
[Unit]
Description=W-UI certificate renewal (acme.sh)
After=network-online.target

[Service]
Type=oneshot
Environment=HOME=${ACME_HOME%/.acme.sh}
ExecStart=$ACME_HOME/acme.sh --cron --home $ACME_HOME
UNIT
    cat > /etc/systemd/system/wui-cert-renew.timer <<UNIT
[Unit]
Description=W-UI certificate renewal check, every six hours

[Timer]
OnCalendar=*-*-* 00/6:00:00
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
UNIT
    systemctl daemon-reload
    systemctl enable --now wui-cert-renew.timer > /dev/null 2>&1 || true
}

install_socat() {
    case "${release}" in
        ubuntu | debian | armbian)
            apt-get update > /dev/null 2>&1 && apt-get install socat -y > /dev/null 2>&1
            ;;
        fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
            dnf makecache -y > /dev/null 2>&1 && dnf -y install socat > /dev/null 2>&1
            ;;
        centos)
            if [[ "${VERSION_ID}" =~ ^7 ]]; then
                yum makecache -y > /dev/null 2>&1 && yum -y install socat > /dev/null 2>&1
            else
                dnf makecache -y > /dev/null 2>&1 && dnf -y install socat > /dev/null 2>&1
            fi
            ;;
        arch | manjaro | parch)
            pacman -Sy --noconfirm socat > /dev/null 2>&1
            ;;
        opensuse-tumbleweed | opensuse-leap)
            zypper refresh > /dev/null 2>&1 && zypper -q install -y socat > /dev/null 2>&1
            ;;
        alpine)
            apk add socat curl openssl > /dev/null 2>&1
            ;;
        *)
            LOGW "Unsupported OS for automatic socat installation"
            ;;
    esac
}

# Certificate files the panel's own account can read. acme.sh keeps its
# state under root; the copies the panel serves live here.
cert_dir_for() {
    echo "$CERT_ROOT/$1"
}

prepare_cert_dir() {
    install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$CERT_ROOT" 2> /dev/null || mkdir -p "$CERT_ROOT"
    install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$1" 2> /dev/null || mkdir -p "$1"
}

fix_cert_perms() {
    chown "$SERVICE_USER:$SERVICE_USER" "$1"/privkey.pem "$1"/fullchain.pem 2> /dev/null
    chmod 600 "$1"/privkey.pem 2> /dev/null
    chmod 644 "$1"/fullchain.pem 2> /dev/null
}

# Hand the panel a certificate: what `x-ui cert -webCert -webCertKey` is.
set_panel_cert() {
    panel_cli setting set --cert "$1" --key "$2" > /dev/null 2>&1
}

ssl_cert_issue_main() {
    echo -e "${green}\t1.${plain} Get SSL (Domain)"
    echo -e "${green}\t2.${plain} Revoke & Remove"
    echo -e "${green}\t3.${plain} Force Renew"
    echo -e "${green}\t4.${plain} Show Existing Domains"
    echo -e "${green}\t5.${plain} Set Cert paths for the panel"
    echo -e "${green}\t6.${plain} Get SSL for IP Address (6-day cert, auto-renews)"
    echo -e "${green}\t0.${plain} Back to Main Menu"

    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            ssl_cert_issue
            ssl_cert_issue_main
            ;;
        2)
            local domains=$(find "$CERT_ROOT"/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found to revoke."
            else
                echo "Existing domains:"
                echo "$domains"
                read -rp "Please enter a domain from the list to revoke and remove the certificate: " domain
                if echo "$domains" | grep -qw "$domain"; then
                    # The IP-cert flow (option 6) stores files under certs/ip, but acme.sh
                    # tracks the cert under the actual IP address(es). Resolve those so renewal
                    # state is torn down too; otherwise the renewal re-creates the deleted cert.
                    local acme_ids="${domain}"
                    if [[ "${domain}" == "ip" ]]; then
                        acme_ids=$(acme --list 2> /dev/null | awk 'NR>1 {print $1}' | grep -E '^([0-9]{1,3}\.){3}[0-9]{1,3}$|:')
                    fi
                    for id in ${acme_ids}; do
                        # Best-effort revoke at the CA, then drop acme.sh renewal tracking.
                        acme --revoke -d "${id}" 2> /dev/null
                        acme --remove -d "${id}" 2> /dev/null
                        # --remove leaves the cert files on disk, so delete the state dirs (RSA + ECC).
                        rm -rf "$ACME_HOME/${id}" "$ACME_HOME/${id}_ecc"
                    done
                    # Delete the local certificate files for this domain.
                    rm -rf "$CERT_ROOT/${domain}"
                    LOGI "Certificate revoked and removed for domain: ${domain}"

                    # If the panel currently serves this domain's cert, clear the stored paths
                    # so it stops loading the now-deleted files, then restart.
                    local existing_cert=$(setting_value cert)
                    if [[ "${existing_cert}" == "$CERT_ROOT/${domain}/"* ]]; then
                        panel_cli setting set --no-tls > /dev/null 2>&1
                        LOGI "Cleared panel certificate paths referencing ${domain}; restarting panel."
                        restart
                    fi
                else
                    echo "Invalid domain entered."
                fi
            fi
            ssl_cert_issue_main
            ;;
        3)
            local domains=$(find "$CERT_ROOT"/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found to renew."
            else
                echo "Existing domains:"
                echo "$domains"
                read -rp "Please enter a domain from the list to renew the SSL certificate: " domain
                if echo "$domains" | grep -qw "$domain"; then
                    local acme_ids="${domain}"
                    if [[ "${domain}" == "ip" ]]; then
                        acme_ids=$(acme --list 2> /dev/null | awk 'NR>1 {print $1}' | grep -E '^([0-9]{1,3}\.){3}[0-9]{1,3}$|:')
                    fi
                    for id in ${acme_ids}; do
                        acme --renew -d ${id} --force
                    done
                    LOGI "Certificate forcefully renewed for domain: $domain"
                else
                    echo "Invalid domain entered."
                fi
            fi
            ssl_cert_issue_main
            ;;
        4)
            local domains=$(find "$CERT_ROOT"/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found under $CERT_ROOT."
            else
                echo "Existing domains and their paths:"
                for domain in $domains; do
                    local cert_path="$CERT_ROOT/${domain}/fullchain.pem"
                    local key_path="$CERT_ROOT/${domain}/privkey.pem"
                    if [[ -f "${cert_path}" && -f "${key_path}" ]]; then
                        echo -e "Domain: ${domain}"
                        echo -e "\tCertificate Path: ${cert_path}"
                        echo -e "\tPrivate Key Path: ${key_path}"
                        if command -v openssl > /dev/null 2>&1; then
                            echo -e "\tExpires: $(openssl x509 -in "${cert_path}" -noout -enddate 2> /dev/null | cut -d= -f2)"
                        fi
                    else
                        echo -e "Domain: ${domain} - Certificate or Key missing."
                    fi
                done
            fi
            # The panel's configured certificate may live outside $CERT_ROOT
            # (e.g. certbot under /etc/letsencrypt) — show it too.
            local panel_cert=$(setting_value cert)
            if [[ -n "${panel_cert}" && "${panel_cert}" != "$CERT_ROOT"/* ]]; then
                echo -e "Panel certificate (custom path): ${panel_cert}"
                if [[ -f "${panel_cert}" ]] && command -v openssl > /dev/null 2>&1; then
                    local panel_sans=$(openssl x509 -in "${panel_cert}" -noout -ext subjectAltName 2> /dev/null \
                        | grep -Eo '(DNS|IP Address):[^,[:space:]]+' | cut -d: -f2- | tr '\n' ' ')
                    [[ -n "${panel_sans}" ]] && echo -e "\tCovers: ${panel_sans}"
                fi
            fi
            echo
            systemctl list-timers wui-cert-renew.timer --no-pager 2> /dev/null | sed 's/^/  /'
            ssl_cert_issue_main
            ;;
        5)
            echo -e "${green}\t1.${plain} Use a certificate from $CERT_ROOT"
            echo -e "${green}\t2.${plain} Enter custom certificate file paths (e.g. certbot, /etc/letsencrypt/...)"
            read -rp "Choose an option: " pathChoice
            if [[ "$pathChoice" == "2" ]]; then
                read -rp "Certificate file path (fullchain): " webCertFile
                read -rp "Private key file path: " webKeyFile
                if [[ -f "${webCertFile}" && -f "${webKeyFile}" ]]; then
                    if ! sudo -u "$SERVICE_USER" test -r "${webKeyFile}" 2> /dev/null; then
                        LOGW "The panel runs as ${SERVICE_USER}, which cannot read ${webKeyFile}; giving it access"
                        setfacl -m "u:${SERVICE_USER}:r" "${webKeyFile}" "${webCertFile}" 2> /dev/null \
                            || chgrp "$SERVICE_USER" "${webKeyFile}" "${webCertFile}" 2> /dev/null
                        chmod g+r "${webKeyFile}" "${webCertFile}" 2> /dev/null
                    fi
                    set_panel_cert "$webCertFile" "$webKeyFile"
                    echo "Panel certificate paths set:"
                    echo "  - Certificate File: $webCertFile"
                    echo "  - Private Key File: $webKeyFile"
                    restart
                else
                    echo "Certificate or private key file not found."
                fi
                ssl_cert_issue_main
                return
            fi
            local domains=$(find "$CERT_ROOT"/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found."
            else
                echo "Available domains:"
                echo "$domains"
                read -rp "Please choose a domain to set the panel paths: " domain

                if echo "$domains" | grep -qw "$domain"; then
                    local webCertFile="$CERT_ROOT/${domain}/fullchain.pem"
                    local webKeyFile="$CERT_ROOT/${domain}/privkey.pem"

                    if [[ -f "${webCertFile}" && -f "${webKeyFile}" ]]; then
                        set_panel_cert "$webCertFile" "$webKeyFile"
                        echo "Panel paths set for domain: $domain"
                        echo "  - Certificate File: $webCertFile"
                        echo "  - Private Key File: $webKeyFile"
                        # Register the acme.sh install-cert hook so auto-renewal copies the
                        # renewed cert to these paths and reloads the panel. Without it acme.sh
                        # renews but never updates the copies, silently serving a stale cert.
                        if [[ -x "$ACME_HOME/acme.sh" ]] && acme --list 2> /dev/null | awk '{print $1}' | grep -Fxq "${domain}"; then
                            acme --installcert --force -d "${domain}" \
                                --key-file "${webKeyFile}" \
                                --fullchain-file "${webCertFile}" \
                                --reloadcmd "w-ui restart" 2>&1 || true
                            fix_cert_perms "$CERT_ROOT/${domain}"
                            echo "Registered acme.sh auto-renewal hook for ${domain}."
                        fi
                        restart
                    else
                        echo "Certificate or private key not found for domain: $domain."
                    fi
                else
                    echo "Invalid domain entered."
                fi
            fi
            ssl_cert_issue_main
            ;;
        6)
            echo -e "${yellow}Let's Encrypt SSL Certificate for IP Address${plain}"
            echo -e "This will obtain a certificate for your server's IP using the shortlived profile."
            echo -e "${yellow}Certificate valid for ~6 days, auto-renews via acme.sh cron job.${plain}"
            echo -e "${yellow}Port 80 must be open and accessible from the internet.${plain}"
            confirm "Do you want to proceed?" "y"
            if [[ $? == 0 ]]; then
                ssl_cert_issue_for_ip
            fi
            ssl_cert_issue_main
            ;;

        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            ssl_cert_issue_main
            ;;
    esac
}

ssl_cert_issue_for_ip() {
    LOGI "Starting automatic SSL certificate generation for server IP..."
    LOGI "Using Let's Encrypt shortlived profile (~6 days validity, auto-renews)"

    local existing_webBasePath=$(setting_value basePath)
    local existing_port=$(setting_value port)

    # Get server IP
    local server_ip
    server_ip=$(detect_server_ip)

    if [[ -n "$server_ip" ]]; then
        LOGI "Server IP detected: ${server_ip}"
        if ! confirm "Is ${server_ip} the correct incoming public IPv4 address for this server?" "y"; then
            server_ip=""
        fi
    else
        LOGI "Could not auto-detect server IP from any provider."
    fi

    [[ -z "$server_ip" ]] && server_ip=$(ask_server_ip)

    LOGI "Issuing certificate for server IP: ${server_ip}"

    # Ask for optional IPv6
    local ipv6_addr=""
    read -rp "Do you have an IPv6 address to include? (leave empty to skip): " ipv6_addr
    ipv6_addr="${ipv6_addr// /}" # Trim whitespace

    # check for acme.sh first
    if [[ ! -x "$ACME_HOME/acme.sh" ]]; then
        LOGI "acme.sh not found, installing..."
        install_acme
        if [ $? -ne 0 ]; then
            LOGE "Failed to install acme.sh"
            return 1
        fi
    fi

    # install socat
    install_socat

    # Create certificate directory
    certPath="$(cert_dir_for ip)"
    prepare_cert_dir "$certPath"

    # Build domain arguments
    local domain_args="-d ${server_ip}"
    if [[ -n "$ipv6_addr" ]] && is_ipv6 "$ipv6_addr"; then
        domain_args="${domain_args} -d ${ipv6_addr}"
        LOGI "Including IPv6 address: ${ipv6_addr}"
    fi

    # Choose port for HTTP-01 listener (default 80, allow override)
    local WebPort=""
    read -rp "Port to use for ACME HTTP-01 listener (default 80): " WebPort
    WebPort="${WebPort:-80}"
    if ! [[ "${WebPort}" =~ ^[0-9]+$ ]] || ((WebPort < 1 || WebPort > 65535)); then
        LOGE "Invalid port provided. Falling back to 80."
        WebPort=80
    fi
    LOGI "Using port ${WebPort} to issue certificate for IP: ${server_ip}"
    if [[ "${WebPort}" -ne 80 ]]; then
        LOGI "Reminder: Let's Encrypt still reaches port 80; forward external port 80 to ${WebPort} for validation."
    fi

    while true; do
        if is_port_in_use "${WebPort}"; then
            LOGI "Port ${WebPort} is currently in use."

            local alt_port=""
            read -rp "Enter another port for acme.sh standalone listener (leave empty to abort): " alt_port
            alt_port="${alt_port// /}"
            if [[ -z "${alt_port}" ]]; then
                LOGE "Port ${WebPort} is busy; cannot proceed with issuance."
                return 1
            fi
            if ! [[ "${alt_port}" =~ ^[0-9]+$ ]] || ((alt_port < 1 || alt_port > 65535)); then
                LOGE "Invalid port provided."
                return 1
            fi
            WebPort="${alt_port}"
            continue
        else
            LOGI "Port ${WebPort} is free and ready for standalone validation."
            break
        fi
    done

    # Reload command - fixes ownership after renewal; the panel re-reads the
    # files itself, so nothing has to restart.
    local reloadCmd="chown ${SERVICE_USER}:${SERVICE_USER} ${certPath}/privkey.pem ${certPath}/fullchain.pem; chmod 600 ${certPath}/privkey.pem"

    # issue the certificate for IP with shortlived profile
    acme --set-default-ca --server letsencrypt --force
    acme --issue \
        ${domain_args} \
        --standalone \
        --server letsencrypt \
        --certificate-profile shortlived \
        --days 6 \
        --httpport ${WebPort} \
        --force

    if [ $? -ne 0 ]; then
        LOGE "Failed to issue certificate for IP: ${server_ip}"
        LOGE "Make sure port ${WebPort} is open and the server is accessible from the internet"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf "$ACME_HOME/${server_ip}" "$ACME_HOME/${server_ip}_ecc" 2> /dev/null
        [[ -n "$ipv6_addr" ]] && rm -rf "$ACME_HOME/${ipv6_addr}" "$ACME_HOME/${ipv6_addr}_ecc" 2> /dev/null
        rm -rf ${certPath} 2> /dev/null
        return 1
    else
        LOGI "Certificate issued successfully for IP: ${server_ip}"
    fi

    # Install the certificate
    # Note: acme.sh may report "Reload error" and exit non-zero if reloadcmd fails,
    # but the cert files are still installed. We check for files instead of exit code.
    acme --installcert --force -d ${server_ip} \
        --key-file "${certPath}/privkey.pem" \
        --fullchain-file "${certPath}/fullchain.pem" \
        --reloadcmd "${reloadCmd}" 2>&1 || true

    # Verify certificate files exist (don't rely on exit code - reloadcmd failure causes non-zero)
    if [[ ! -f "${certPath}/fullchain.pem" || ! -f "${certPath}/privkey.pem" ]]; then
        LOGE "Certificate files not found after installation"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf "$ACME_HOME/${server_ip}" "$ACME_HOME/${server_ip}_ecc" 2> /dev/null
        [[ -n "$ipv6_addr" ]] && rm -rf "$ACME_HOME/${ipv6_addr}" "$ACME_HOME/${ipv6_addr}_ecc" 2> /dev/null
        rm -rf ${certPath} 2> /dev/null
        return 1
    fi

    LOGI "Certificate files installed successfully"

    # enable auto-renew
    acme --upgrade --auto-upgrade > /dev/null 2>&1
    ensure_renewal
    fix_cert_perms "$certPath"

    # Prompt user to set panel paths after successful certificate installation
    local webCertFile="${certPath}/fullchain.pem"
    local webKeyFile="${certPath}/privkey.pem"

    read -rp "Would you like to set this certificate for the panel? (y/n): " setPanel
    if [[ "$setPanel" == "y" || "$setPanel" == "Y" ]]; then
        if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
            set_panel_cert "$webCertFile" "$webKeyFile"
            LOGI "Panel paths set for IP: $server_ip"
            LOGI "  - Certificate File: $webCertFile"
            LOGI "  - Private Key File: $webKeyFile"
            LOGI "  - Validity: ~6 days (auto-renews via acme.sh, checked every 6 hours)"
            echo -e "${green}Access URL: https://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
            LOGI "Panel will restart to apply SSL certificate..."
            restart
        else
            LOGE "Error: Certificate or private key file not found for IP: $server_ip."
            return 1
        fi
    else
        LOGI "Skipping panel path setting."
    fi

    return 0
}

ssl_cert_issue() {
    local existing_webBasePath=$(setting_value basePath)
    local existing_port=$(setting_value port)
    # check for acme.sh first
    if [[ ! -x "$ACME_HOME/acme.sh" ]]; then
        echo "acme.sh could not be found. we will install it"
        install_acme
        if [ $? -ne 0 ]; then
            LOGE "install acme failed, please check logs"
            exit 1
        fi
    fi

    # install socat
    install_socat
    if [ $? -ne 0 ]; then
        LOGE "install socat failed, please check logs"
        exit 1
    else
        LOGI "install socat succeed..."
    fi

    # get the domain here, and we need to verify it
    local domain=""
    while true; do
        read -rp "Please enter your domain name: " domain
        domain="${domain// /}" # Trim whitespace

        if [[ -z "$domain" ]]; then
            LOGE "Domain name cannot be empty. Please try again."
            continue
        fi

        if ! is_domain "$domain"; then
            LOGE "Invalid domain format: ${domain}. Please enter a valid domain name."
            continue
        fi

        break
    done
    LOGD "Your domain is: ${domain}, checking it..."
    SSL_ISSUED_DOMAIN="${domain}"

    # detect existing certificate and reuse it only if its files are actually
    # present and non-empty. acme.sh stores ECC certs under ${domain}_ecc and RSA
    # certs under ${domain}; a failed issuance can leave a domain entry in --list
    # with no usable cert files, which must not be reused (it produces a 0-byte
    # fullchain.pem). Broken partial state is cleaned up so issuance can proceed.
    local cert_exists=0
    if acme --list 2> /dev/null | awk '{print $1}' | grep -Fxq "${domain}"; then
        local acmeCertDir=""
        if [[ -s "$ACME_HOME/${domain}_ecc/fullchain.cer" && -s "$ACME_HOME/${domain}_ecc/${domain}.key" ]]; then
            acmeCertDir="$ACME_HOME/${domain}_ecc"
        elif [[ -s "$ACME_HOME/${domain}/fullchain.cer" && -s "$ACME_HOME/${domain}/${domain}.key" ]]; then
            acmeCertDir="$ACME_HOME/${domain}"
        fi
        if [[ -n "${acmeCertDir}" ]]; then
            cert_exists=1
            local certInfo=$(acme --list 2> /dev/null | grep -F "${domain}")
            LOGI "Existing certificate found for ${domain}, will reuse it."
            [[ -n "${certInfo}" ]] && LOGI "${certInfo}"
        else
            LOGW "Found incomplete acme.sh state for ${domain} (no valid certificate files); cleaning it up and re-issuing."
            rm -rf "$ACME_HOME/${domain}" "$ACME_HOME/${domain}_ecc"
        fi
    fi
    if [[ ${cert_exists} -eq 0 ]]; then
        LOGI "Your domain is ready for issuing certificates now..."
    fi

    # create a directory for the certificate
    certPath="$(cert_dir_for "$domain")"
    if [ ! -d "$certPath" ]; then
        prepare_cert_dir "$certPath"
    else
        rm -rf "$certPath"
        prepare_cert_dir "$certPath"
    fi

    # get the port number for the standalone server
    local WebPort=80
    read -rp "Please choose which port to use (default is 80): " WebPort
    if [[ -z ${WebPort} ]]; then
        WebPort=80
    elif [[ ! ${WebPort} =~ ^[1-9][0-9]*$ || ${WebPort} -gt 65535 ]]; then
        LOGE "Your input ${WebPort} is invalid, will use default port 80."
        WebPort=80
    fi
    LOGI "Will use port: ${WebPort} to issue certificates. Please make sure this port is open."

    if [[ ${cert_exists} -eq 0 ]]; then
        # issue the certificate
        acme --set-default-ca --server letsencrypt --force
        acme --issue -d ${domain} $(acme_listen_flag) --standalone --httpport ${WebPort} --force
        if [ $? -ne 0 ]; then
            LOGE "Issuing certificate failed, please check logs."
            rm -rf "$ACME_HOME/${domain}" "$ACME_HOME/${domain}_ecc"
            exit 1
        else
            LOGE "Issuing certificate succeeded, installing certificates..."
        fi
    else
        LOGI "Using existing certificate, installing certificates..."
    fi

    reloadCmd="w-ui restart"

    LOGI "Default --reloadcmd for ACME is: ${yellow}w-ui restart"
    LOGI "This command will run on every certificate issue and renew."
    read -rp "Would you like to modify --reloadcmd for ACME? (y/n): " setReloadcmd
    if [[ "$setReloadcmd" == "y" || "$setReloadcmd" == "Y" ]]; then
        echo -e "\n${green}\t1.${plain} Preset: systemctl reload nginx ; w-ui restart"
        echo -e "${green}\t2.${plain} Input your own command"
        echo -e "${green}\t0.${plain} Keep default reloadcmd"
        read -rp "Choose an option: " choice
        case "$choice" in
            1)
                LOGI "Reloadcmd is: systemctl reload nginx ; w-ui restart"
                reloadCmd="systemctl reload nginx ; w-ui restart"
                ;;
            2)
                LOGD "It's recommended to put w-ui restart at the end, so it won't raise an error if other services fails"
                read -rp "Please enter your reloadcmd (example: systemctl reload nginx ; w-ui restart): " reloadCmd
                LOGI "Your reloadcmd is: ${reloadCmd}"
                ;;
            *)
                LOGI "Keep default reloadcmd"
                ;;
        esac
    fi
    # Whatever was chosen, the renewed files must stay readable by the panel.
    reloadCmd="chown ${SERVICE_USER}:${SERVICE_USER} ${certPath}/privkey.pem ${certPath}/fullchain.pem; ${reloadCmd}"

    # install the certificate
    local installOutput=""
    installOutput=$(acme --installcert --force -d ${domain} \
        --key-file ${certPath}/privkey.pem \
        --fullchain-file ${certPath}/fullchain.pem --reloadcmd "${reloadCmd}" 2>&1)
    local installRc=$?
    echo "${installOutput}"

    local installWroteFiles=0
    if echo "${installOutput}" | grep -q "Installing key to:" && echo "${installOutput}" | grep -q "Installing full chain to:"; then
        installWroteFiles=1
    fi

    if [[ -f "${certPath}/privkey.pem" && -f "${certPath}/fullchain.pem" && (${installRc} -eq 0 || ${installWroteFiles} -eq 1) ]]; then
        LOGI "Installing certificate succeeded, enabling auto renew..."
    else
        LOGE "Installing certificate failed, exiting."
        if [[ ${cert_exists} -eq 0 ]]; then
            rm -rf "$ACME_HOME/${domain}" "$ACME_HOME/${domain}_ecc"
        fi
        exit 1
    fi

    # enable auto-renew
    acme --upgrade --auto-upgrade
    ensure_renewal
    fix_cert_perms "$certPath"
    if [ $? -ne 0 ]; then
        LOGE "Auto renew failed, certificate details:"
        ls -lah "$certPath"/*
        exit 1
    else
        LOGI "Auto renew succeeded, certificate details:"
        ls -lah "$certPath"/*
    fi

    # Prompt user to set panel paths after successful certificate installation
    read -rp "Would you like to set this certificate for the panel? (y/n): " setPanel
    if [[ "$setPanel" == "y" || "$setPanel" == "Y" ]]; then
        local webCertFile="${certPath}/fullchain.pem"
        local webKeyFile="${certPath}/privkey.pem"

        if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
            set_panel_cert "$webCertFile" "$webKeyFile"
            LOGI "Panel paths set for domain: $domain"
            LOGI "  - Certificate File: $webCertFile"
            LOGI "  - Private Key File: $webKeyFile"
            echo -e "${green}Access URL: https://${domain}:${existing_port}${existing_webBasePath}${plain}"
            restart
        else
            LOGE "Error: Certificate or private key file not found for domain: $domain."
        fi
    else
        LOGI "Skipping panel path setting."
    fi
}

ssl_cert_issue_CF() {
    local existing_webBasePath=$(setting_value basePath)
    local existing_port=$(setting_value port)
    LOGI "****** Instructions for Use ******"
    LOGI "Follow the steps below to complete the process:"
    LOGI "1. A Cloudflare API Token (recommended, scoped to Zone:DNS:Edit) or the Global API Key + registered email."
    LOGI "2. The Domain Name."
    LOGI "3. Once the certificate is issued, you will be prompted to set the certificate for the panel (optional)."
    LOGI "4. The script also supports automatic renewal of the SSL certificate after installation."

    confirm "Do you confirm the information and wish to proceed? [y/n]" "y"

    if [ $? -eq 0 ]; then
        # Check for acme.sh first
        if [[ ! -x "$ACME_HOME/acme.sh" ]]; then
            echo "acme.sh could not be found. We will install it."
            install_acme
            if [ $? -ne 0 ]; then
                LOGE "Install acme failed, please check logs."
                exit 1
            fi
        fi

        CF_Domain=""

        LOGD "Please set a domain name:"
        read -rp "Input your domain here: " CF_Domain
        LOGD "Your domain name is set to: ${CF_Domain}"

        # Cloudflare API credentials: an API Token (recommended, scoped to a
        # single zone) or the account-wide Global API Key. acme.sh reads
        # CF_Token for tokens, or CF_Key + CF_Email for the Global Key.
        CF_KeyType=""
        read -rp "Are you using a Cloudflare API Token or Global API Key? (t/g) [Default t]: " CF_KeyType
        CF_KeyType=${CF_KeyType:-t}

        if [[ "$CF_KeyType" == "g" || "$CF_KeyType" == "G" ]]; then
            CF_GlobalKey=""
            CF_AccountEmail=""
            LOGD "Please set the Global API Key:"
            read -rp "Input your key here: " CF_GlobalKey
            LOGD "Please set up the registered email:"
            read -rp "Input your email here: " CF_AccountEmail
            export CF_Key="${CF_GlobalKey}"
            export CF_Email="${CF_AccountEmail}"
        else
            CF_ApiToken=""
            LOGD "Please set the API Token:"
            read -rp "Input your token here: " CF_ApiToken
            export CF_Token="${CF_ApiToken}"
        fi

        # Set the default CA to Let's Encrypt
        acme --set-default-ca --server letsencrypt --force
        if [ $? -ne 0 ]; then
            LOGE "Default CA, Let'sEncrypt fail, script exiting..."
            exit 1
        fi

        # Issue the certificate using Cloudflare DNS
        acme --issue --dns dns_cf -d ${CF_Domain} -d *.${CF_Domain} --log --force
        if [ $? -ne 0 ]; then
            LOGE "Certificate issuance failed, script exiting..."
            exit 1
        else
            LOGI "Certificate issued successfully, Installing..."
        fi

        # Install the certificate
        certPath="$(cert_dir_for "$CF_Domain")"
        if [ -d "$certPath" ]; then
            rm -rf ${certPath}
        fi

        prepare_cert_dir "$certPath"
        if [ $? -ne 0 ]; then
            LOGE "Failed to create directory: ${certPath}"
            exit 1
        fi

        reloadCmd="w-ui restart"

        LOGI "Default --reloadcmd for ACME is: ${yellow}w-ui restart"
        LOGI "This command will run on every certificate issue and renew."
        read -rp "Would you like to modify --reloadcmd for ACME? (y/n): " setReloadcmd
        if [[ "$setReloadcmd" == "y" || "$setReloadcmd" == "Y" ]]; then
            echo -e "\n${green}\t1.${plain} Preset: systemctl reload nginx ; w-ui restart"
            echo -e "${green}\t2.${plain} Input your own command"
            echo -e "${green}\t0.${plain} Keep default reloadcmd"
            read -rp "Choose an option: " choice
            case "$choice" in
                1)
                    LOGI "Reloadcmd is: systemctl reload nginx ; w-ui restart"
                    reloadCmd="systemctl reload nginx ; w-ui restart"
                    ;;
                2)
                    LOGD "It's recommended to put w-ui restart at the end, so it won't raise an error if other services fails"
                    read -rp "Please enter your reloadcmd (example: systemctl reload nginx ; w-ui restart): " reloadCmd
                    LOGI "Your reloadcmd is: ${reloadCmd}"
                    ;;
                *)
                    LOGI "Keep default reloadcmd"
                    ;;
            esac
        fi
        reloadCmd="chown ${SERVICE_USER}:${SERVICE_USER} ${certPath}/privkey.pem ${certPath}/fullchain.pem; ${reloadCmd}"
        acme --installcert --force -d ${CF_Domain} -d *.${CF_Domain} \
            --key-file ${certPath}/privkey.pem \
            --fullchain-file ${certPath}/fullchain.pem --reloadcmd "${reloadCmd}"

        if [ $? -ne 0 ]; then
            LOGE "Certificate installation failed, script exiting..."
            exit 1
        else
            LOGI "Certificate installed successfully, Turning on automatic updates..."
        fi

        # Enable auto-update
        acme --upgrade --auto-upgrade
        ensure_renewal
        fix_cert_perms "$certPath"
        if [ $? -ne 0 ]; then
            LOGE "Auto update setup failed, script exiting..."
            exit 1
        else
            LOGI "The certificate is installed and auto-renewal is turned on. Specific information is as follows:"
            ls -lah ${certPath}/*
        fi

        # Prompt user to set panel paths after successful certificate installation
        read -rp "Would you like to set this certificate for the panel? (y/n): " setPanel
        if [[ "$setPanel" == "y" || "$setPanel" == "Y" ]]; then
            local webCertFile="${certPath}/fullchain.pem"
            local webKeyFile="${certPath}/privkey.pem"

            if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
                set_panel_cert "$webCertFile" "$webKeyFile"
                LOGI "Panel paths set for domain: $CF_Domain"
                LOGI "  - Certificate File: $webCertFile"
                LOGI "  - Private Key File: $webKeyFile"
                echo -e "${green}Access URL: https://${CF_Domain}:${existing_port}${existing_webBasePath}${plain}"
                restart
            else
                LOGE "Error: Certificate or private key file not found for domain: $CF_Domain."
            fi
        else
            LOGI "Skipping panel path setting."
        fi
    else
        show_menu
    fi
}

run_speedtest() {
    # Check if Speedtest is already installed
    if ! command -v speedtest &> /dev/null; then
        # If not installed, determine installation method
        if command -v snap &> /dev/null; then
            # Use snap to install Speedtest
            echo "Installing Speedtest using snap..."
            snap install speedtest
        else
            # Fallback to using package managers
            local pkg_manager=""
            local speedtest_install_script=""

            if command -v dnf &> /dev/null; then
                pkg_manager="dnf"
                speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh"
            elif command -v yum &> /dev/null; then
                pkg_manager="yum"
                speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh"
            elif command -v apt-get &> /dev/null; then
                pkg_manager="apt-get"
                speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh"
            elif command -v apt &> /dev/null; then
                pkg_manager="apt"
                speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh"
            fi

            if [[ -z $pkg_manager ]]; then
                echo "Error: Package manager not found. You may need to install Speedtest manually."
                return 1
            else
                echo "Installing Speedtest using $pkg_manager..."
                curl -s $speedtest_install_script | bash
                $pkg_manager install -y speedtest
            fi
        fi
    fi

    speedtest
}

ip_validation() {
    ipv6_regex="^(([0-9a-fA-F]{1,4}:){7,7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))$"
    ipv4_regex="^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]?|0)\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]?|0)$"
}

# IP Limit: fail2ban watching the panel's own log for refused sign-ins, and
# banning the address that keeps trying. The device limit on a plan is
# enforced by the panel itself, so the jail here guards the sign-in form.
iplimit_main() {
    echo -e "\n${green}\t1.${plain} Install Fail2ban and configure IP Limit"
    echo -e "${green}\t2.${plain} Change Ban Duration"
    echo -e "${green}\t3.${plain} Unban Everyone"
    echo -e "${green}\t4.${plain} Ban Logs"
    echo -e "${green}\t5.${plain} Ban an IP Address"
    echo -e "${green}\t6.${plain} Unban an IP Address"
    echo -e "${green}\t7.${plain} Real-Time Logs"
    echo -e "${green}\t8.${plain} Service Status"
    echo -e "${green}\t9.${plain} Service Restart"
    echo -e "${green}\t10.${plain} Uninstall Fail2ban and IP Limit"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            confirm "Proceed with installation of Fail2ban & IP Limit?" "y"
            if [[ $? == 0 ]]; then
                install_iplimit
            else
                iplimit_main
            fi
            ;;
        2)
            read -rp "Please enter new Ban Duration in Minutes [default 30]: " NUM
            if [[ $NUM =~ ^[0-9]+$ ]]; then
                create_iplimit_jails ${NUM}
                systemctl restart fail2ban
            else
                echo -e "${red}${NUM} is not a number! Please, try again.${plain}"
            fi
            iplimit_main
            ;;
        3)
            confirm "Proceed with Unbanning everyone from IP Limit jail?" "y"
            if [[ $? == 0 ]]; then
                fail2ban-client reload --restart --unban w-ui-ipl
                truncate -s 0 "${iplimit_banned_log_path}"
                echo -e "${green}All users Unbanned successfully.${plain}"
                iplimit_main
            else
                echo -e "${yellow}Cancelled.${plain}"
            fi
            iplimit_main
            ;;
        4)
            show_banlog
            iplimit_main
            ;;
        5)
            read -rp "Enter the IP address you want to ban: " ban_ip
            ip_validation
            if [[ $ban_ip =~ $ipv4_regex || $ban_ip =~ $ipv6_regex ]]; then
                fail2ban-client set w-ui-ipl banip "$ban_ip"
                echo -e "${green}IP Address ${ban_ip} has been banned successfully.${plain}"
            else
                echo -e "${red}Invalid IP address format! Please try again.${plain}"
            fi
            iplimit_main
            ;;
        6)
            read -rp "Enter the IP address you want to unban: " unban_ip
            ip_validation
            if [[ $unban_ip =~ $ipv4_regex || $unban_ip =~ $ipv6_regex ]]; then
                fail2ban-client set w-ui-ipl unbanip "$unban_ip"
                echo -e "${green}IP Address ${unban_ip} has been unbanned successfully.${plain}"
            else
                echo -e "${red}Invalid IP address format! Please try again.${plain}"
            fi
            iplimit_main
            ;;
        7)
            tail -f /var/log/fail2ban.log
            iplimit_main
            ;;
        8)
            service fail2ban status
            iplimit_main
            ;;
        9)
            systemctl restart fail2ban
            iplimit_main
            ;;
        10)
            remove_iplimit
            iplimit_main
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            iplimit_main
            ;;
    esac
}

setup_fail2ban_iplimit() {
    if ! command -v fail2ban-client &> /dev/null; then
        echo -e "${green}Fail2ban is not installed. Installing now...!${plain}\n"

        # Install fail2ban together with nftables. Recent fail2ban packages
        # default to `banaction = nftables-multiport`, but the `nftables`
        # package isn't pulled in as a dependency on most minimal images.
        case "${release}" in
            ubuntu)
                apt-get update
                if [[ "${os_version}" -ge 2400 ]]; then
                    apt-get install python3-pip -y
                    python3 -m pip install pyasynchat --break-system-packages
                fi
                apt-get install fail2ban nftables -y
                ;;
            debian)
                apt-get update
                if [ "$os_version" -ge 12 ]; then
                    apt-get install -y python3-systemd
                fi
                apt-get install -y fail2ban nftables
                ;;
            armbian)
                apt-get update && apt-get install fail2ban nftables -y
                ;;
            fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
                if [[ "${release}" != "fedora" ]] && ! dnf repolist enabled 2> /dev/null | grep -qiw epel; then
                    dnf install -y epel-release \
                        || dnf install -y "https://dl.fedoraproject.org/pub/epel/epel-release-latest-$(rpm -E %rhel).noarch.rpm" \
                        || echo -e "${yellow}Could not enable the EPEL repository; fail2ban is only available from EPEL on this distro.${plain}"
                fi
                dnf makecache -y && dnf -y install fail2ban nftables
                ;;
            centos)
                if [[ "${VERSION_ID}" =~ ^7 ]]; then
                    yum makecache -y && yum install epel-release -y
                    yum -y install fail2ban nftables
                else
                    dnf makecache -y && dnf -y install fail2ban nftables
                fi
                ;;
            arch | manjaro | parch)
                pacman -Sy --noconfirm fail2ban nftables
                ;;
            alpine)
                apk add fail2ban nftables
                ;;
            *)
                echo -e "${red}Unsupported operating system. Please check the script and install the necessary packages manually.${plain}\n"
                return 1
                ;;
        esac

        if ! command -v fail2ban-client &> /dev/null; then
            echo -e "${red}Fail2ban installation failed.${plain}\n"
            return 1
        fi

        echo -e "${green}Fail2ban installed successfully!${plain}\n"
    else
        echo -e "${yellow}Fail2ban is already installed.${plain}\n"
    fi

    echo -e "${green}Configuring IP Limit...${plain}\n"

    # make sure there's no conflict for jail files
    iplimit_remove_conflicts

    # Check if log file exists
    if ! test -f "${iplimit_banned_log_path}"; then
        touch ${iplimit_banned_log_path}
    fi

    # Create the iplimit jail files
    # we didn't pass the bantime here to use the default value
    create_iplimit_jails

    # Launching fail2ban
    if ! systemctl is-active --quiet fail2ban; then
        systemctl start fail2ban
    else
        systemctl restart fail2ban
    fi
    systemctl enable fail2ban

    echo -e "${green}IP Limit installed and configured successfully!${plain}\n"
    return 0
}

# install_iplimit is the interactive (menu) entry point: it runs the shared
# setup and then returns to the menu.
install_iplimit() {
    setup_fail2ban_iplimit
    before_show_menu
}

remove_iplimit() {
    echo -e "${green}\t1.${plain} Only remove IP Limit configurations"
    echo -e "${green}\t2.${plain} Uninstall Fail2ban and IP Limit"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " num
    case "$num" in
        1)
            rm -f /etc/fail2ban/filter.d/w-ui-ipl.conf
            rm -f /etc/fail2ban/action.d/w-ui-ipl.conf
            rm -f /etc/fail2ban/jail.d/w-ui-ipl.conf
            systemctl restart fail2ban
            echo -e "${green}IP Limit removed successfully!${plain}\n"
            before_show_menu
            ;;
        2)
            rm -rf /etc/fail2ban
            systemctl stop fail2ban
            case "${release}" in
                ubuntu | debian | armbian)
                    apt-get remove -y fail2ban
                    apt-get purge -y fail2ban -y
                    apt-get autoremove -y
                    ;;
                fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
                    dnf remove fail2ban -y
                    dnf autoremove -y
                    ;;
                centos)
                    if [[ "${VERSION_ID}" =~ ^7 ]]; then
                        yum remove fail2ban -y
                        yum autoremove -y
                    else
                        dnf remove fail2ban -y
                        dnf autoremove -y
                    fi
                    ;;
                arch | manjaro | parch)
                    pacman -Rns --noconfirm fail2ban
                    ;;
                alpine)
                    apk del fail2ban
                    ;;
                *)
                    echo -e "${red}Unsupported operating system. Please uninstall Fail2ban manually.${plain}\n"
                    exit 1
                    ;;
            esac
            echo -e "${green}Fail2ban and IP Limit removed successfully!${plain}\n"
            before_show_menu
            ;;
        0)
            show_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            remove_iplimit
            ;;
    esac
}

show_banlog() {
    local system_log="/var/log/fail2ban.log"

    echo -e "${green}Checking ban logs...${plain}\n"

    if ! systemctl is-active --quiet fail2ban; then
        echo -e "${red}Fail2ban service is not running!${plain}\n"
        return 1
    fi

    if [[ -f "$system_log" ]]; then
        echo -e "${green}Recent system ban activities from fail2ban.log:${plain}"
        grep "w-ui-ipl" "$system_log" | grep -E "Ban|Unban" | tail -n 10 || echo -e "${yellow}No recent system ban activities found${plain}"
        echo ""
    fi

    if [[ -f "${iplimit_banned_log_path}" ]]; then
        echo -e "${green}W-UI-IPL ban log entries:${plain}"
        if [[ -s "${iplimit_banned_log_path}" ]]; then
            grep -v "INIT" "${iplimit_banned_log_path}" | tail -n 10 || echo -e "${yellow}No ban entries found${plain}"
        else
            echo -e "${yellow}Ban log file is empty${plain}"
        fi
    else
        echo -e "${red}Ban log file not found at: ${iplimit_banned_log_path}${plain}"
    fi

    echo -e "\n${green}Current jail status:${plain}"
    fail2ban-client status w-ui-ipl || echo -e "${yellow}Unable to get jail status${plain}"
}

create_iplimit_jails() {
    # Use default bantime if not passed => 30 minutes
    local bantime="${1:-30}"

    # Uncomment 'allowipv6 = auto' in fail2ban.conf
    sed -i 's/#allowipv6 = auto/allowipv6 = auto/g' /etc/fail2ban/fail2ban.conf

    # The panel logs to the journal, so the jail reads the journal.
    cat << EOF > /etc/fail2ban/jail.d/w-ui-ipl.conf
[w-ui-ipl]
enabled=true
backend=systemd
journalmatch=_SYSTEMD_UNIT=${SERVICE}.service
filter=w-ui-ipl
action=w-ui-ipl
maxretry=5
findtime=10m
bantime=${bantime}m
EOF

    # The panel's own line for a refused sign-in:
    #   level=WARN msg="failed sign-in" username=x ip=1.2.3.4 lockout=...
    cat << EOF > /etc/fail2ban/filter.d/w-ui-ipl.conf
[Definition]
failregex   = msg="failed sign-in".*\bip=<ADDR>\b
ignoreregex =
EOF

    # Only the panel's port is closed to a banned address: a customer whose
    # neighbour on a shared address guessed passwords keeps their tunnel, and
    # the administrator keeps SSH.
    local panel_port
    panel_port=$(setting_value port)
    [[ -n "${panel_port}" ]] || panel_port=2096

    cat << EOF > /etc/fail2ban/action.d/w-ui-ipl.conf
[INCLUDES]
before = iptables-multiport.conf

[Definition]
actionstart = <iptables> -N f2b-<name>
              <iptables> -A f2b-<name> -j <returntype>
              <iptables> -I <chain> -p <protocol> -m multiport --dports <port> -j f2b-<name>

actionstop = <iptables> -D <chain> -p <protocol> -m multiport --dports <port> -j f2b-<name>
             <actionflush>
             <iptables> -X f2b-<name>

actioncheck = <iptables> -n -L <chain> | grep -q 'f2b-<name>[ \t]'

actionban = <iptables> -I f2b-<name> 1 -s <ip> -j <blocktype>
            echo "\$(date +"%%Y/%%m/%%d %%H:%%M:%%S")   BAN   [IP] = <ip> banned for <bantime> seconds." >> ${iplimit_banned_log_path}

actionunban = <iptables> -D f2b-<name> -s <ip> -j <blocktype>
              echo "\$(date +"%%Y/%%m/%%d %%H:%%M:%%S")   UNBAN   [IP] = <ip> unbanned." >> ${iplimit_banned_log_path}

[Init]
name = default
chain = INPUT
port = ${panel_port}
protocol = tcp
EOF

    echo -e "${green}Ip Limit jail files created with a bantime of ${bantime} minutes.${plain}"
}

iplimit_remove_conflicts() {
    local jail_files=(
        /etc/fail2ban/jail.conf
        /etc/fail2ban/jail.local
    )

    for file in "${jail_files[@]}"; do
        # Check for [w-ui-ipl] config in jail file then remove it
        if test -f "${file}" && grep -qw 'w-ui-ipl' ${file}; then
            sed -i "/\[w-ui-ipl\]/,/^$/d" ${file}
            echo -e "${yellow}Removing conflicts of [w-ui-ipl] in jail (${file})!${plain}\n"
        fi
    done
}

SSH_port_forwarding() {
    local server_ip
    server_ip=$(detect_server_ip)

    if [[ -z "$server_ip" ]]; then
        echo -e "${yellow}Could not auto-detect server IP from any provider.${plain}"
        server_ip=$(ask_server_ip)
    fi

    local existing_webBasePath=$(setting_value basePath)
    local existing_port=$(setting_value port)
    local existing_listenIP=$(setting_value listenIP)
    local existing_cert=$(setting_value cert)
    local existing_key=$(setting_value key)

    local config_listenIP=""
    local listen_choice=""

    if [[ -n "$existing_cert" && -n "$existing_key" ]]; then
        echo -e "${green}Panel is secure with SSL.${plain}"
        before_show_menu
    fi
    if [[ -z "$existing_cert" && -z "$existing_key" && (-z "$existing_listenIP" || "$existing_listenIP" == "0.0.0.0") ]]; then
        echo -e "\n${red}Warning: No Cert and Key found! The panel is not secure.${plain}"
        echo "Please obtain a certificate or set up SSH port forwarding."
    fi

    if [[ -n "$existing_listenIP" && "$existing_listenIP" != "0.0.0.0" && (-z "$existing_cert" && -z "$existing_key") ]]; then
        echo -e "\n${green}Current SSH Port Forwarding Configuration:${plain}"
        echo -e "Standard SSH command:"
        echo -e "${yellow}ssh -L 2222:${existing_listenIP}:${existing_port} root@${server_ip}${plain}"
        echo -e "\nIf using SSH key:"
        echo -e "${yellow}ssh -i <sshkeypath> -L 2222:${existing_listenIP}:${existing_port} root@${server_ip}${plain}"
        echo -e "\nAfter connecting, access the panel at:"
        echo -e "${yellow}http://localhost:2222${existing_webBasePath}${plain}"
    fi

    echo -e "\nChoose an option:"
    echo -e "${green}1.${plain} Set listen IP"
    echo -e "${green}2.${plain} Clear listen IP"
    echo -e "${green}0.${plain} Back to Main Menu"
    read -rp "Choose an option: " num

    case "$num" in
        1)
            if [[ -z "$existing_listenIP" || "$existing_listenIP" == "0.0.0.0" ]]; then
                echo -e "\nNo listenIP configured. Choose an option:"
                echo -e "1. Use default IP (127.0.0.1)"
                echo -e "2. Set a custom IP"
                read -rp "Select an option (1 or 2): " listen_choice

                config_listenIP="127.0.0.1"
                [[ "$listen_choice" == "2" ]] && read -rp "Enter custom IP to listen on: " config_listenIP

                panel_cli setting set --listen "${config_listenIP}" > /dev/null 2>&1
                echo -e "${green}listen IP has been set to ${config_listenIP}.${plain}"
                echo -e "\n${green}SSH Port Forwarding Configuration:${plain}"
                echo -e "Standard SSH command:"
                echo -e "${yellow}ssh -L 2222:${config_listenIP}:${existing_port} root@${server_ip}${plain}"
                echo -e "\nIf using SSH key:"
                echo -e "${yellow}ssh -i <sshkeypath> -L 2222:${config_listenIP}:${existing_port} root@${server_ip}${plain}"
                echo -e "\nAfter connecting, access the panel at:"
                echo -e "${yellow}http://localhost:2222${existing_webBasePath}${plain}"
                restart
            else
                config_listenIP="${existing_listenIP}"
                echo -e "${green}Current listen IP is already set to ${config_listenIP}.${plain}"
            fi
            ;;
        2)
            panel_cli setting set --listen 0.0.0.0 > /dev/null 2>&1
            echo -e "${green}Listen IP has been cleared.${plain}"
            restart
            ;;
        0)
            show_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            SSH_port_forwarding
            ;;
    esac
}

# Backup & Restore sits where 3x-ui's PostgreSQL menu is: this panel keeps
# everything in one SQLite file, and the archive is the whole of it.
backup_menu() {
    echo -e "\n${green}\t1.${plain} Create a backup"
    echo -e "${green}\t2.${plain} Restore from a backup"
    echo -e "${green}\t3.${plain} List backups"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    echo -e "  ${yellow}The archive holds the database, every interface key and every${plain}"
    echo -e "  ${yellow}customer credential. Treat it like a password file.${plain}"
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
            backup_menu
            ;;
        3)
            ls -lh /root/wui-backup-*.tar.gz 2> /dev/null | sed 's/^/  /' || echo -e "  ${yellow}No backups yet${plain}"
            backup_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            backup_menu
            ;;
    esac
}

show_usage() {
    echo -e "┌────────────────────────────────────────────────────────────────┐
│  ${blue}w-ui control menu usages (subcommands):${plain}                       │
│                                                                │
│  ${blue}w-ui${plain}                       - Admin Management Script          │
│  ${blue}w-ui start${plain}                 - Start                            │
│  ${blue}w-ui stop${plain}                  - Stop                             │
│  ${blue}w-ui restart${plain}               - Restart                          │
│  ${blue}w-ui restart-tunnels${plain}       - Restart every tunnel             │
│  ${blue}w-ui status${plain}                - Current Status                   │
│  ${blue}w-ui settings${plain}              - Current Settings                 │
│  ${blue}w-ui enable${plain}                - Enable Autostart on OS Startup   │
│  ${blue}w-ui disable${plain}               - Disable Autostart on OS Startup  │
│  ${blue}w-ui log${plain}                   - Check logs                       │
│  ${blue}w-ui banlog${plain}                - Check Fail2ban ban logs          │
│  ${blue}w-ui update${plain}                - Update                           │
│  ${blue}w-ui update-dev${plain}            - Update to Dev channel (latest)   │
│  ${blue}w-ui update-all-geofiles${plain}   - Update all geo files             │
│  ${blue}w-ui backup${plain}                - Backup & Restore                 │
│  ${blue}w-ui legacy${plain}                - Legacy version                   │
│  ${blue}w-ui install${plain}               - Install                          │
│  ${blue}w-ui uninstall${plain}             - Uninstall                        │
└────────────────────────────────────────────────────────────────┘"
}

show_menu() {
    echo -e "
╔────────────────────────────────────────────────╗
│  ${green}W-UI Panel Management Script${plain}                  │
│  ${green}0.${plain} Exit Script                               │
│────────────────────────────────────────────────│
│  ${green}1.${plain} Install                                   │
│  ${green}2.${plain} Update                                    │
│  ${green}3.${plain} Update to Dev Channel (latest commit)     │
│  ${green}4.${plain} Update Menu                               │
│  ${green}5.${plain} Legacy Version                            │
│  ${green}6.${plain} Uninstall                                 │
│────────────────────────────────────────────────│
│  ${green}7.${plain} Reset Username & Password                 │
│  ${green}8.${plain} Reset Web Base Path                       │
│  ${green}9.${plain} Reset Settings                            │
│  ${green}10.${plain} Change Port                              │
│  ${green}11.${plain} View Current Settings                    │
│────────────────────────────────────────────────│
│  ${green}12.${plain} Start                                    │
│  ${green}13.${plain} Stop                                     │
│  ${green}14.${plain} Restart                                  │
│  ${green}15.${plain} Restart Tunnels                          │
│  ${green}16.${plain} Check Status                             │
│  ${green}17.${plain} Logs Management                          │
│────────────────────────────────────────────────│
│  ${green}18.${plain} Enable Autostart                         │
│  ${green}19.${plain} Disable Autostart                        │
│────────────────────────────────────────────────│
│  ${green}20.${plain} SSL Certificate Management               │
│  ${green}21.${plain} Cloudflare SSL Certificate               │
│  ${green}22.${plain} IP Limit Management                      │
│  ${green}23.${plain} Firewall Management                      │
│  ${green}24.${plain} SSH Port Forwarding Management           │
│  ${green}25.${plain} Backup & Restore                         │
│────────────────────────────────────────────────│
│  ${green}26.${plain} Enable BBR                               │
│  ${green}27.${plain} Update Geo Files                         │
│  ${green}28.${plain} Speedtest by Ookla                       │
╚────────────────────────────────────────────────╝
"
    show_status
    echo && read -rp "Please enter your selection [0-28]: " num

    case "${num}" in
        0)
            exit 0
            ;;
        1)
            check_uninstall && install
            ;;
        2)
            check_install && update
            ;;
        3)
            check_install && update_dev
            ;;
        4)
            check_install && update_menu
            ;;
        5)
            check_install && legacy_version
            ;;
        6)
            check_install && uninstall
            ;;
        7)
            check_install && reset_user
            ;;
        8)
            check_install && reset_webbasepath
            ;;
        9)
            check_install && reset_config
            ;;
        10)
            check_install && set_port
            ;;
        11)
            check_install && check_config
            ;;
        12)
            check_install && start
            ;;
        13)
            check_install && stop
            ;;
        14)
            check_install && restart
            ;;
        15)
            check_install && restart_tunnels
            ;;
        16)
            check_install && status
            ;;
        17)
            check_install && show_log
            ;;
        18)
            check_install && enable
            ;;
        19)
            check_install && disable
            ;;
        20)
            ssl_cert_issue_main
            ;;
        21)
            ssl_cert_issue_CF
            ;;
        22)
            iplimit_main
            ;;
        23)
            firewall_menu
            ;;
        24)
            SSH_port_forwarding
            ;;
        25)
            check_install && backup_menu
            ;;
        26)
            bbr_menu
            ;;
        27)
            update_geo
            ;;
        28)
            run_speedtest
            ;;
        *)
            LOGE "Please enter the correct number [0-28]"
            ;;
    esac
}

if [[ $# > 0 ]]; then
    case $1 in
        "start")
            check_install 0 && start 0
            ;;
        "stop")
            check_install 0 && stop 0
            ;;
        "restart")
            check_install 0 && restart 0
            ;;
        "restart-tunnels")
            check_install 0 && restart_tunnels 0
            ;;
        "status")
            check_install 0 && status 0
            ;;
        "settings")
            check_install 0 && check_config 0
            ;;
        "enable")
            check_install 0 && enable 0
            ;;
        "disable")
            check_install 0 && disable 0
            ;;
        "log")
            check_install 0 && show_log 0
            ;;
        "banlog")
            check_install 0 && show_banlog 0
            ;;
        "setup-fail2ban")
            setup_fail2ban_iplimit
            ;;
        "update")
            check_install 0 && update 0
            ;;
        "update-dev")
            check_install 0 && update_dev 0
            ;;
        "legacy")
            check_install 0 && legacy_version 0
            ;;
        "install")
            check_uninstall 0 && install 0
            ;;
        "uninstall")
            check_install 0 && uninstall 0
            ;;
        "update-all-geofiles")
            geo_updated=0
            if check_install 0 && update_geofiles 0; then
                [[ $geo_updated -eq 0 ]] || restart 0
            fi
            ;;
        "backup")
            check_install 0 && backup_menu
            ;;
        *) show_usage ;;
    esac
else
    show_menu
fi
