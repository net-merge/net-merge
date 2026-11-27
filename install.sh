#!/bin/sh
# Installs net-merge on macOS or Linux, the easiest way the machine allows:
#
#   curl -fsSL https://raw.githubusercontent.com/net-merge/net-merge/main/install.sh | sh
#
# Linux: the system's package manager (apt, dnf, yum, zypper, apk, pacman) from net-merge's signed
# repositories, so it updates with everything else; otherwise Homebrew; otherwise the release tarball.
# macOS: Homebrew if it is installed, otherwise the release tarball.
# Then it starts the tray. NET_MERGE_METHOD=tarball skips the package managers, NET_MERGE_NO_START=1
# leaves the tray off. Running it again updates an installed net-merge.
#
# Uninstall, however it was installed:
#
#   curl -fsSL https://raw.githubusercontent.com/net-merge/net-merge/main/install.sh | sh -s -- uninstall
set -eu

REPO=https://github.com/net-merge/net-merge
BASE=https://pkg.net-merge.org
FPR=0168C7AB77721656904811AE904F61E7F4F1C2A1

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf 'net-merge install: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
fetch() { # fetch URL: the body on stdout
    if have curl; then curl -fsSL "$1"; elif have wget; then wget -qO- "$1"; else die "needs curl or wget"; fi
}

if [ "$(id -u)" = 0 ]; then SUDO=
elif have sudo; then SUDO=sudo
elif have doas; then SUDO=doas
else die "run it as root, or install sudo"; fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# ---- the release tarball, for any CPU net-merge is built for
tarball() {
    if [ "$(uname -s)" = Darwin ]; then
        name=macos-universal
    else
        case $(uname -m) in
            i?86) name=linux-i686 ;;
            armv8l) name=linux-armv7l ;;
            *) name=linux-$(uname -m) ;;
        esac
    fi
    say "Downloading net-merge ($name)"
    fetch "$REPO/releases/latest/download/net-merge-$name.tar.gz" | tar -xz -C "$tmp" net-merge ||
        die "no download for $name; see $REPO/releases"
    if [ "$(uname -s)" = Darwin ]; then
        $SUDO "$tmp/net-merge" install   # copies it to /usr/local/bin, lets the tray start without a password
    else
        $SUDO install -D -m 755 "$tmp/net-merge" /usr/local/bin/net-merge
        if have setcap; then
            $SUDO setcap cap_net_admin,cap_net_raw+ep /usr/local/bin/net-merge
        else
            say "setcap not found: install libcap, then run: sudo setcap cap_net_admin,cap_net_raw+ep /usr/local/bin/net-merge"
        fi
    fi
}

# ---- Homebrew (as the user: brew refuses root)
homebrew() {
    say "Installing with Homebrew"
    if brew list --formula net-merge >/dev/null 2>&1; then
        brew update --quiet
        brew upgrade net-merge/tap/net-merge
    else
        brew install net-merge/tap/net-merge
    fi
    bin=$(brew --prefix)/bin/net-merge
    if [ "$(uname -s)" = Darwin ]; then
        $SUDO "$bin" install
    else
        $SUDO setcap cap_net_admin,cap_net_raw+ep "$(readlink -f "$bin")"
    fi
}

# ---- the system package manager, from the signed repositories
apt_install() {
    say "Installing with apt"
    fetch "$BASE/apt/key.gpg" > "$tmp/key.gpg"
    $SUDO install -D -m 644 "$tmp/key.gpg" /usr/share/keyrings/net-merge.gpg
    echo "deb [signed-by=/usr/share/keyrings/net-merge.gpg] $BASE/apt stable main" | $SUDO tee /etc/apt/sources.list.d/net-merge.list >/dev/null
    $SUDO apt-get update -qq
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq net-merge
}
rpm_install() { # rpm_install dnf|yum
    say "Installing with $1"
    fetch "$BASE/rpm/net-merge.repo" > "$tmp/net-merge.repo"
    $SUDO install -m 644 "$tmp/net-merge.repo" /etc/yum.repos.d/net-merge.repo
    $SUDO "$1" clean expire-cache >/dev/null   # a cached package list would hide a new release
    $SUDO "$1" install -y net-merge
    $SUDO "$1" upgrade -y net-merge            # install leaves an installed one as it is
}
zypper_install() {
    say "Installing with zypper"
    $SUDO zypper --non-interactive lr net-merge >/dev/null 2>&1 || $SUDO zypper --non-interactive addrepo "$BASE/rpm/net-merge.repo"
    $SUDO zypper --non-interactive --gpg-auto-import-keys refresh net-merge
    $SUDO zypper --non-interactive install net-merge
}
apk_install() {
    say "Installing with apk"
    fetch "$BASE/apk/net-merge.rsa.pub" > "$tmp/net-merge.rsa.pub"
    $SUDO install -m 644 "$tmp/net-merge.rsa.pub" /etc/apk/keys/net-merge.rsa.pub
    grep -qx "$BASE/apk" /etc/apk/repositories || echo "$BASE/apk" | $SUDO tee -a /etc/apk/repositories >/dev/null
    $SUDO apk add --update-cache --upgrade net-merge   # also updates an installed one
}
pacman_install() {
    say "Installing with pacman"
    # a fresh system may have no local pacman key yet, which signing ours needs
    $SUDO pacman-key --list-secret-keys 2>/dev/null | grep -q '^sec' || $SUDO pacman-key --init
    if ! $SUDO pacman-key --list-keys "$FPR" >/dev/null 2>&1; then
        fetch "$BASE/apt/key.asc" > "$tmp/key.asc"
        $SUDO pacman-key --add "$tmp/key.asc"
        $SUDO pacman-key --lsign-key "$FPR"
    fi
    grep -q '^\[net-merge\]' /etc/pacman.conf ||
        printf '\n[net-merge]\nSigLevel = Required\nServer = %s/arch/$arch\n' "$BASE" | $SUDO tee -a /etc/pacman.conf >/dev/null
    $SUDO pacman -Sy --needed --noconfirm net-merge
}

# ---- uninstall: stop it, then remove it the way it was installed, with the repository it came from
uninstall() {
    say "Stopping net-merge"
    if have net-merge; then
        net-merge quit >/dev/null 2>&1 || true           # the tray
        $SUDO net-merge off >/dev/null 2>&1 || true      # system-wide mode started from a terminal
        net-merge autostart off >/dev/null 2>&1 || true  # start at sign-in
        sleep 2
    fi
    if [ "$(uname -s)" = Darwin ]; then
        for exe in /opt/net-merge/bin/net-merge /usr/local/bin/net-merge; do
            [ -x "$exe" ] && { $SUDO "$exe" uninstall || true; break; }   # the root-owned copy and the sudoers rule
        done
        if have brew && brew list --formula net-merge >/dev/null 2>&1; then
            say "Removing with Homebrew"
            brew uninstall net-merge
            brew untap net-merge/tap >/dev/null 2>&1 || true
            brew untap rohit267/tap >/dev/null 2>&1 || true   # the old name of the tap
        fi
        $SUDO rm -rf /var/run/net-merge "/Library/Application Support/net-merge" "$HOME/Library/Logs/net-merge.log"
    else
        if have dpkg && dpkg -s net-merge >/dev/null 2>&1; then
            say "Removing with apt"
            $SUDO env DEBIAN_FRONTEND=noninteractive apt-get remove -y -qq net-merge
            $SUDO rm -f /etc/apt/sources.list.d/net-merge.list /usr/share/keyrings/net-merge.gpg
        elif have rpm && rpm -q net-merge >/dev/null 2>&1; then
            if have zypper; then
                say "Removing with zypper"
                $SUDO zypper --non-interactive remove net-merge
                $SUDO zypper --non-interactive removerepo net-merge >/dev/null 2>&1 || true
            else
                pm=dnf; have dnf || pm=yum
                say "Removing with $pm"
                $SUDO "$pm" remove -y net-merge
                $SUDO rm -f /etc/yum.repos.d/net-merge.repo
            fi
        elif have apk && apk info -e net-merge >/dev/null 2>&1; then
            say "Removing with apk"
            $SUDO apk del net-merge
            $SUDO sed -i "\#^$BASE/apk\$#d" /etc/apk/repositories
            $SUDO rm -f /etc/apk/keys/net-merge.rsa.pub
        elif have pacman && pacman -Q net-merge >/dev/null 2>&1; then
            say "Removing with pacman"
            $SUDO pacman -R --noconfirm net-merge
            $SUDO sed -i '/^\[net-merge\]$/,/^Server = .*net-merge/d' /etc/pacman.conf
            $SUDO pacman-key --delete "$FPR" >/dev/null 2>&1 || true
        fi
        if have brew && brew list --formula net-merge >/dev/null 2>&1; then
            say "Removing with Homebrew"
            brew uninstall net-merge
            brew untap net-merge/tap >/dev/null 2>&1 || true
            brew untap rohit267/tap >/dev/null 2>&1 || true   # the old name of the tap
        fi
        $SUDO rm -f /usr/local/bin/net-merge   # the tarball
        rm -rf "${XDG_CACHE_HOME:-$HOME/.cache}/net-merge"
    fi
    hash -r 2>/dev/null || true
    have net-merge && die "a net-merge is still at $(command -v net-merge); remove it by hand"
    say "net-merge is uninstalled. Your network settings are back to normal."
}

case ${1:-} in
    uninstall|--uninstall) uninstall; exit 0 ;;
    '') ;;
    *) die "unknown option $1 (use: uninstall)" ;;
esac

# ---- pick the way
method=${NET_MERGE_METHOD:-auto}
if [ "$(uname -s)" = Darwin ]; then
    # a running tray holds the old binary: stop it before replacing it
    pgrep -qf 'net-merge tray' && { net-merge quit 2>/dev/null || true; sleep 2; }
    $SUDO mkdir -p /usr/local/bin   # where `net-merge install` puts its root-owned copy
    if [ "$method" = auto ] && have brew && [ "$(id -u)" != 0 ]; then homebrew; else tarball; fi
elif [ "$(uname -s)" = Linux ]; then
    if [ -f /etc/NIXOS ]; then
        die "on NixOS use the module instead: $REPO#nixos"
    elif [ "$method" != auto ]; then tarball
    elif have apt-get && have dpkg && case $(dpkg --print-architecture) in amd64|arm64|armhf|i386|loong64|ppc64el|riscv64|s390x) true ;; *) false ;; esac; then apt_install
    elif have dnf; then rpm_install dnf
    elif have yum; then rpm_install yum
    elif have zypper; then zypper_install
    elif have apk; then apk_install
    elif have pacman; then pacman_install
    elif have brew && [ "$(id -u)" != 0 ]; then homebrew
    else tarball; fi
else
    die "this script is for macOS and Linux; on Windows run in PowerShell: irm https://raw.githubusercontent.com/net-merge/net-merge/main/install.ps1 | iex"
fi

have net-merge || PATH=$PATH:/usr/local/bin
say "Installed $(net-merge version)"

# ---- start the tray: on a Mac, or on a Linux desktop session that isn't root's
# a tray that is still running has the old version: it is stopped only once the new one is in place,
# and only where it can be started again
running=
have pgrep && pgrep -f 'net-merge tray' >/dev/null && running=1
if [ "${NET_MERGE_NO_START:-}" = 1 ]; then
    say "Start it with: net-merge tray${running:+ (quit the running one first)}"
elif [ "$(uname -s)" = Darwin ] || { [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ] && [ "$(id -u)" != 0 ]; }; then
    [ -n "$running" ] && { net-merge quit >/dev/null 2>&1 || true; sleep 2; }
    nohup net-merge tray >/dev/null 2>&1 &
    say "Started: look for the net-merge icon in the menu bar or tray. To start it at sign-in: net-merge autostart on"
else
    say "Start it with: net-merge tray (from your desktop session), or net-merge run (any terminal, as root)"
    [ -n "$running" ] && say "The running net-merge is still the old version: quit it and start it again"
fi
