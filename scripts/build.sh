#!/bin/bash
# Compila RsyncGUI 2.3 per la piattaforma corrente.
#
# Schema del progetto: sourcecode/ e build/ restano separati, dentro la cartella
# della versione. Wails però pretende una build/ nella root del progetto: la si
# crea al volo da assets/ e la si cancella sempre a fine script (anche in caso
# di errore, via trap), così sourcecode/ non contiene mai artefatti.
#
# Su macOS produce DUE app separate (Apple Silicon e Intel): ognuna ha un
# CFBundleIdentifier e un CFBundleName diversi, altrimenti macOS le tratta come
# la stessa app e LaunchServices ne risolve una sola — era il motivo per cui la
# versione arm64 veniva rilevata come Intel.
#
# Richiede Go (~/.local/go se installato user-local) e il CLI di Wails.
set -euo pipefail
cd "$(dirname "$0")/.."

# Toolchain in ~/.local/go; GOPATH in AiWorkspace (vedi ~/.zshrc). Il fallback
# copre il default di Go, se la shell non esporta GOPATH.
export PATH="$HOME/.local/go/bin:${GOPATH:-$HOME/go}/bin:$PATH"

if ! command -v wails >/dev/null; then
  echo "Wails CLI non trovato. Installa con:"
  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"
  exit 1
fi

OUT_DIR="../build"
mkdir -p "${OUT_DIR}"

# Area di lavoro temporanea richiesta da Wails, rimossa a fine script.
cleanup() { rm -rf build; }
trap cleanup EXIT
rm -rf build
mkdir -p build
cp -R assets/. build/

# build_mac <platform> <nome .app> <bundle id> <nome visualizzato> <min macOS>
build_mac() {
  local platform="$1" appname="$2" bundleid="$3" label="$4" minos="$5"
  local dest="${OUT_DIR}/${appname}.app"

  echo
  echo "▸ Build ${label} (${platform})"
  wails build -platform "${platform}"

  rm -rf "${dest}"
  cp -R build/bin/RsyncGUI.app "${dest}"

  local plist="${dest}/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier ${bundleid}" "${plist}"
  /usr/libexec/PlistBuddy -c "Set :CFBundleName ${label}" "${plist}"
  /usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersion ${minos}" "${plist}"

  # Modificare l'Info.plist invalida la firma fatta da Wails: va rifirmata,
  # altrimenti macOS rifiuta di aprire l'app ("è danneggiata").
  codesign --force --deep --sign - "${dest}" 2>/dev/null

  codesign --verify --deep "${dest}" && echo "  firma:        ok"
  echo "  architettura: $(lipo -archs "${dest}/Contents/MacOS/RsyncGUI")"
  echo "  bundle id:    $(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${plist}")"
  echo "  → ${dest}"
}

case "$(uname -s)" in
  Darwin)
    # Ogni app è compilata per una sola architettura: un binario solo-arm64 è
    # riconosciuto da macOS come "Applicazione (Apple Silicon)", uno solo-x86_64
    # come "(Intel)". Niente universal binary, così restano nettamente distinte.
    build_mac "darwin/arm64" "RsyncGUI-AppleSilicon" "com.wails.rsyncgui.arm64" "RsyncGUI-AppleSilicon" "11.0"
    build_mac "darwin/amd64" "RsyncGUI-Intel"        "com.wails.rsyncgui.intel" "RsyncGUI-Intel"        "10.13.0"
    echo
    echo "Fatto. Due app separate in ${OUT_DIR}/"
    ;;
  Linux)
    wails build -platform linux/amd64
    rm -f "${OUT_DIR}/RsyncGUI"
    cp build/bin/RsyncGUI "${OUT_DIR}/RsyncGUI"
    echo "Fatto. Output in ${OUT_DIR}/RsyncGUI"
    ;;
  MINGW*|MSYS*|CYGWIN*)
    wails build -platform windows/amd64
    rm -f "${OUT_DIR}/RsyncGUI.exe"
    cp build/bin/RsyncGUI.exe "${OUT_DIR}/RsyncGUI.exe"
    echo "Fatto. Output in ${OUT_DIR}/RsyncGUI.exe"
    ;;
  *)
    echo "Piattaforma non riconosciuta"; exit 1
    ;;
esac
