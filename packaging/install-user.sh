#!/bin/sh
# Installs V4Lens' desktop integration for the current user: the
# application desktop entry and the icon theme entry.
#
# Required on native-Wayland sessions: GLFW selects the Wayland backend at
# runtime, and Wayland has no per-window icon property — the compositor
# resolves window and taskbar icons from the desktop entry whose ID matches
# the app_id (in.skobk.in.v4lens). Under X11/XWayland the process sets
# _NET_WM_ICON itself and this installation is not needed for icons
# (the entry still enables launcher integration).
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

apps_dir="$HOME/.local/share/applications"
icons_dir="$HOME/.local/share/icons/hicolor/scalable/apps"

mkdir -p "$apps_dir" "$icons_dir"
cp "$here/in.skobk.in.v4lens.desktop" "$apps_dir/"
cp "$here/icons/in.skobk.in.v4lens.svg" "$icons_dir/"

# Best-effort cache refresh for the running desktop; every tool is optional.
update-desktop-database "$apps_dir" 2>/dev/null || true
gtk-update-icon-cache -q -f -t "$HOME/.local/share/icons/hicolor" 2>/dev/null || true
kbuildsycoca6 2>/dev/null || kbuildsycoca5 2>/dev/null || true

echo "Installed: $apps_dir/in.skobk.in.v4lens.desktop"
echo "Installed: $icons_dir/in.skobk.in.v4lens.svg"
