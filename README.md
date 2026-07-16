# RsyncGUI

GUI nativa e multipiattaforma per `rsync`, scritta in Go + [Wails](https://wails.io) — leggera (~8MB), usa la webview di sistema invece di Electron.

[![Build](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml/badge.svg)](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

## Download

Le build compilate (macOS Apple Silicon/Intel, Windows, Linux + AppImage) sono nella pagina **[Release](https://github.com/TrismegistoOpenSource/RsyncGUI/releases)** — non serve installare nulla per compilare, si scarica e si usa.

## Funzionalità

- **Aggiungi / Rimuovi** profili di sincronizzazione, ciascuno con **più sorgenti e più destinazioni**: rsync accetta nativamente più sorgenti in un solo comando, ma una sola destinazione per invocazione, quindi l'app esegue un rsync per ogni destinazione, in sequenza.
- **Tag**: profili con lo stesso tag si avviano tutti insieme con un clic, **in sequenza, uno alla volta**, mai in parallelo.
- **Ordinamento** per tag (alfabetico) o manuale (drag & drop), con preferenza ricordata tra un avvio e l'altro.
- Opzioni rsync per profilo: checksum, elimina extra, simulazione, compressione, output dettagliato, scrittura diretta (`--inplace`) — sempre con `-a` per preservare permessi e timestamp.
- **Esclusioni automatiche** dei file che i sistemi operativi creano da soli (`.DS_Store`, `Thumbs.db`, `desktop.ini`, ecc.), attive di default, più esclusioni personalizzate.
- **Percorsi non disponibili** (volume non montato, mount di rete morto) vengono saltati senza bloccare la coda, segnalati nel log e in un riepilogo a fine esecuzione.
- **Verifica cartella**: scansiona una cartella e segnala i file che il filesystem elenca ma rifiuta di aprire — utile prima di un backup su NAS/cloud con condivisioni di rete inaffidabili.
- **Interrompi** una sincronizzazione in corso con l'equivalente di Ctrl+C (SIGINT, non un kill secco: rsync ha modo di ripulire i file temporanei).
- Log live in un pannello laterale che allarga la finestra invece di coprire l'interfaccia.
- Import/export delle configurazioni in JSON, con backup automatico di una copia prima di ogni salvataggio.

## Dove salva i dati

Un unico `profiles.json`, in una posizione che si adatta all'OS (`os.UserConfigDir`):

| OS | Percorso |
|---|---|
| macOS | `~/Library/Application Support/RsyncGUI/profiles.json` |
| Linux | `~/.config/RsyncGUI/profiles.json` |
| Windows | `%AppData%\RsyncGUI\profiles.json` |

Prima di ogni salvataggio l'app copia il contenuto precedente in `profiles.json.bak` (una sola copia, sovrascritta ogni volta): un salvataggio andato storto è a un `cp` dall'annullamento.

## Compilare dai sorgenti

Prerequisiti: [Go](https://go.dev/dl/) 1.24+ e il CLI di Wails:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

Poi:

```bash
./scripts/build.sh
```

Rileva l'OS e compila. Su macOS produce **due app separate**, ciascuna con architettura pura (niente universal binary) e bundle ID distinto, così macOS non le confonde tra loro:

| App | Architettura |
|---|---|
| `RsyncGUI-AppleSilicon.app` | solo `arm64` |
| `RsyncGUI-Intel.app` | solo `x86_64` |

Dipendenze per piattaforma:
- **macOS**: Xcode Command Line Tools.
- **Linux**: `gcc`, `libgtk-3-dev`, `libwebkit2gtk-4.0-dev`.
- **Windows**: WebView2 (incluso in Windows 10/11). `rsync` non è nativo su Windows: serve nel `PATH` (MSYS2, cwRsync o WSL) — l'app lo segnala all'avvio se manca.

La compilazione va fatta sulla piattaforma di destinazione: il cross-compile con UI nativa non è affidabile con Wails. Per questo le release su GitHub sono compilate da [GitHub Actions](.github/workflows/build.yml) su runner macOS, Windows e Linux reali.

## Struttura del progetto

```
main.go              avvio Wails, opzioni finestra
app.go               backend: store JSON, import/export, runner rsync
app_test.go          test unitari
frontend/dist/       UI (HTML/CSS/JS vanilla, nessun build step)
assets/              icona sorgente e template Info.plist per Wails
scripts/             script di build e generazione icona
```

## Licenza

[GPL-3.0](LICENSE).
