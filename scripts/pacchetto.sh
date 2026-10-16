#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Confeziona ciò che scripts/build.sh ha prodotto in ../build nel pacchetto
# che riceve l'utente, per il sistema su cui gira. Lo usano le prove locali
# (scripts/ci-locale.sh) e GitHub (.github/workflows/build.yml): stessi
# comandi, stesso risultato.
#
#   macOS    ../build/RsyncGUI-AppleSilicon.zip, RsyncGUI-Intel.zip
#   Linux    ../build/RsyncGUI-Linux-x86_64.AppImage
#   Windows  ../build/RsyncGUI-Windows.zip (cartella con exe e LICENZE)
set -euo pipefail
cd "$(dirname "$0")/.."
B=../build

pacchetto_macos() {
  for nome in RsyncGUI-AppleSilicon RsyncGUI-Intel; do
    rm -f "$B/$nome.zip"
    # --keepParent obbligatorio: senza, lo zip contiene Contents/ e non l'app
    ditto -c -k --sequesterRsrc --keepParent "$B/$nome.app" "$B/$nome.zip"
    echo "→ $B/$nome.zip"
  done
}

pacchetto_linux() {
  # appimagetool: quello indicato da APPIMAGETOOL, altrimenti si tiene in
  # ~/.local/bin, scaricato la prima volta (sui runner ogni volta).
  local tool="${APPIMAGETOOL:-$HOME/.local/bin/appimagetool-x86_64.AppImage}"
  local url="https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage"
  if [ ! -x "$tool" ]; then
    mkdir -p "$(dirname "$tool")"
    curl -fsSL -o "$tool.part" "$url"
    chmod +x "$tool.part"
    mv "$tool.part" "$tool"
  fi
  local lavoro A
  lavoro="$(mktemp -d "${TMPDIR:-/tmp}/rg-appimage-XXXXXX")"
  A="$lavoro/RsyncGUI.AppDir"
  mkdir -p "$A/usr/bin" "$A/usr/share/icons/hicolor/256x256/apps"
  cp "$B/RsyncGUI-Linux-amd64/RsyncGUI" "$A/usr/bin/"
  mkdir -p "$A/usr/share/doc"
  cp -R "$B/RsyncGUI-Linux-amd64/LICENZE" "$A/usr/share/doc/rsyncgui"
  cp assets/appicon.png "$A/rsyncgui.png"
  cp assets/appicon.png "$A/usr/share/icons/hicolor/256x256/apps/rsyncgui.png"
  cat > "$A/RsyncGUI.desktop" <<'DESKTOP'
[Desktop Entry]
Name=RsyncGUI
Exec=RsyncGUI
Icon=rsyncgui
Type=Application
Categories=Utility;
StartupWMClass=RsyncGUI
DESKTOP
  ln -s usr/bin/RsyncGUI "$A/AppRun"
  # --appimage-extract-and-run: i runner non hanno FUSE.
  rm -f "$B/RsyncGUI-Linux-x86_64.AppImage"
  ARCH=x86_64 "$tool" --appimage-extract-and-run "$A" "$B/RsyncGUI-Linux-x86_64.AppImage"
  rm -rf "$lavoro"
  echo "→ $B/RsyncGUI-Linux-x86_64.AppImage"
}

pacchetto_windows() {
  # Git Bash non ha zip: il tar di Windows (bsdtar) sceglie il formato
  # dall'estensione (PROBLEMI-NOTI.md §28).
  rm -f "$B/RsyncGUI-Windows.zip"
  /c/Windows/System32/tar.exe -a -c -f "$(cygpath -w "$PWD/$B/RsyncGUI-Windows.zip")" \
    -C "$(cygpath -w "$PWD/$B")" RsyncGUI-Windows
  echo "→ $B/RsyncGUI-Windows.zip"
}

case "$(uname -s)" in
  Darwin) pacchetto_macos ;;
  Linux) pacchetto_linux ;;
  MINGW*|MSYS*|CYGWIN*) pacchetto_windows ;;
  *) echo "Piattaforma non riconosciuta"; exit 1 ;;
esac
