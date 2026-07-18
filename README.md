# RsyncGUI

GUI nativa e multipiattaforma per `rsync`, scritta in Go + [Wails](https://wails.io) — leggera (~8MB), usa la webview di sistema invece di Electron.

[![Build](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml/badge.svg)](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

## Download

Le build compilate (macOS Apple Silicon/Intel, Windows, Linux + AppImage) sono nella pagina **[Release](https://github.com/TrismegistoOpenSource/RsyncGUI/releases)** — non serve installare nulla per compilare, si scarica e si usa.

### macOS: sbloccare l'app al primo avvio

L'app **non è firmata con un Apple Developer ID**, quindi macOS la mette in
quarantena e al primo avvio dice che è danneggiata o che non può essere aperta.
Non è danneggiata: è la quarantena.

Trascina prima l'app in **Applicazioni**, poi incolla nel Terminale la riga che
corrisponde alla versione scaricata:

```bash
xattr -dr com.apple.quarantine /Applications/RsyncGUI-AppleSilicon.app
```

```bash
xattr -dr com.apple.quarantine /Applications/RsyncGUI-Intel.app
```

Va fatto una volta sola, e serve solo per le app scaricate da internet. In
alternativa: clic destro sull'app → **Apri** → di nuovo **Apri**.

> Questa sezione esiste unicamente perché manca una firma Apple riconosciuta
> (che richiede un account Developer a pagamento). Il giorno in cui il progetto
> ne avrà una e le build saranno notarizzate, la quarantena non scatterà più e
> queste istruzioni andranno rimosse.

## Funzionalità

- **Aggiungi / Rimuovi** profili di sincronizzazione, ciascuno con **più sorgenti e più destinazioni**: rsync accetta nativamente più sorgenti in un solo comando, ma una sola destinazione per invocazione, quindi l'app esegue un rsync per ogni destinazione, in sequenza.
- **Tag**: profili con lo stesso tag si avviano tutti insieme con un clic, **in sequenza, uno alla volta**, mai in parallelo.
- **Ordinamento** per tag (alfabetico) o manuale (drag & drop), con preferenza ricordata tra un avvio e l'altro.
- Opzioni rsync per profilo: checksum, elimina extra, simulazione, compressione, output dettagliato, scrittura diretta (`--inplace`) — sempre con `-a` per preservare permessi e timestamp.
- **Ricrea struttura** (per profilo): copiando `/A` in `/B` si ottiene `/B/A/…` invece di riversare il contenuto di `A` direttamente in `/B`. È la regola dello slash finale di rsync resa esplicita. L'interruttore è rosso quando è attivo perché **spegnerlo dopo che il profilo ha già copiato è distruttivo se è attivo anche `--delete`**: la cartella `/B/A` scritta in precedenza diventa "di troppo" e rsync la elimina. L'app chiede conferma prima di lasciarlo spegnere, spiegando cosa succederebbe.
- **Ripristina**: esegue la copia al contrario, dal backup verso la cartella originale. Disponibile solo sui profili con **una sorgente e una destinazione**, perché con più percorsi i file si mescolano nella destinazione e non è più possibile sapere da dove veniva ciascuno. Mostra sempre i percorsi esatti prima di procedere, tiene conto di "ricrea struttura" per sapere dove sta davvero il backup, e chiede ogni volta se applicare `--delete` — con una seconda conferma, perché in questa direzione cancellerebbe dall'originale tutto ciò che è stato creato dopo il backup.
- **Le copie proseguono a finestra chiusa** (dalla 2.3): una copia avviata non appartiene più alla finestra che l'ha lanciata. Chiudere l'app — o un suo crash — non la interrompe; alla riapertura la sezione **Attività** mostra cosa è ancora in corso e permette di seguirlo o interromperlo. Resta comunque **una sola copia alla volta**, come prima. Disattivabile dall'interruttore in Attività.
- **Barra di avanzamento** con percentuale e file trasferiti, sulla card del profilo e nella sezione Attività. La percentuale la calcola rsync stesso, che è l'unico a sapere quanti file toccherà davvero: un conteggio fatto in anticipo sarebbe smentito dal primo file saltato perché già aggiornato. Quando rsync non riporta nulla — una copia incrementale senza modifiche non emette progresso — la barra resta indeterminata invece di mostrare uno zero inventato.
- **Pulizia automatica dei log**: il log di una copia riuscita viene cancellato appena finisce, lasciando solo il riepilogo; si conservano quelli delle copie fallite (30 giorni), con un tetto complessivo sulla cartella. Un backup non deve riempire il disco su cui salva.
- **Notifiche di sistema** al termine di ogni profilo, con l'esito (completato, completato con avvisi, fallito). Un'interruzione volontaria non notifica nulla.
- **Esclusioni automatiche** dei file che i sistemi operativi creano da soli (`.DS_Store`, `Thumbs.db`, `desktop.ini`, ecc.), attive di default, più esclusioni personalizzate.
- **Percorsi non disponibili** (volume non montato, mount di rete morto) vengono saltati senza bloccare la coda, segnalati nel log e in un riepilogo a fine esecuzione.
- **Verifica cartella**: scansiona una cartella e segnala i file che il filesystem elenca ma rifiuta di aprire — utile prima di un backup su NAS/cloud con condivisioni di rete inaffidabili.
- **Interrompi** una sincronizzazione in corso con l'equivalente di Ctrl+C (SIGINT, non un kill secco: rsync ha modo di ripulire i file temporanei).
- Log live in un pannello laterale che allarga la finestra invece di coprire l'interfaccia.
- Import/export delle configurazioni in JSON, con backup automatico di una copia prima di ogni salvataggio.

## Perché è fatta così

Le decisioni di progetto, con le loro ragioni e le alternative scartate, sono in
[docs/decisions.md](docs/decisions.md).

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

[GPL-3.0](LICENSE). Le librerie Go incorporate nel binario e le loro licenze
(tutte permissive) sono elencate in
[THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md).
