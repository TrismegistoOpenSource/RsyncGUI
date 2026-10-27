# RsyncGUI

GUI nativa e multipiattaforma per `rsync`, scritta in Go + [Wails](https://wails.io) — leggera (~8MB), usa la webview di sistema invece di Electron.

[![Build](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml/badge.svg)](https://github.com/TrismegistoOpenSource/RsyncGUI/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Download

Le build compilate (macOS Apple Silicon/Intel, Windows, Linux + AppImage) sono nella pagina **[Release](https://github.com/TrismegistoOpenSource/RsyncGUI/releases)** — non serve installare nulla per compilare, si scarica e si usa. Serve però **rsync**: su macOS e Linux c'è già, su Windows va installato (vedi sotto).

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

### Windows: installare rsync (una volta sola)

L'app **non include rsync**, e Windows non ne ha uno: su un PC appena installato
RsyncGUI si apre, ma non può copiare finché rsync non c'è (all'avvio lo segnala e
offre i collegamenti). La strada provata è **MSYS2**, che porta con sé anche `ssh`
(serve per le destinazioni remote e per pCloud):

1. Scarica e installa MSYS2 da [msys2.org](https://www.msys2.org/) (cartella
   predefinita `C:\msys64`).
2. Apri **MSYS2 MSYS** dal menu Start e lancia:
   ```bash
   pacman -S rsync openssh
   ```
3. Aggiungi `C:\msys64\usr\bin` al `Path` di Windows: **Start** → «Modifica le
   variabili di ambiente per l'account» → `Path` → **Modifica** → **Nuovo** →
   `C:\msys64\usr\bin`. Mettila **in fondo** alla lista, così gli strumenti di
   MSYS2 non prendono il posto di quelli di Windows.
4. **Chiudi e riapri RsyncGUI** (un programma già aperto non vede il `Path`
   nuovo). Per controllare, in PowerShell: `rsync --version` e `ssh -V`.
5. Solo per le destinazioni remote e pCloud: se `ssh -V` risponde
   `OpenSSH_for_Windows…`, vince l'`ssh` di Windows (`C:\Windows\System32\OpenSSH`),
   che con rsync di MSYS2 non è stato provato. Per usare quello di MSYS2 metti
   `C:\msys64\usr\bin` **sopra** quella voce, nel `Path` **di sistema** (serve
   l'amministratore: il `Path` dell'account viene sempre dopo) e riapri l'app (`ssh -V` deve dire
   `OpenSSH_10…` o simile, senza «for_Windows»).

Alternative: [cwRsync](https://itefix.net/cwrsync), con la sua cartella nel `Path`
allo stesso modo; oppure WSL. L'`rsync` deve essere GNU rsync (o openrsync) e, per
pCloud, nella stessa cartella dev'esserci un `ssh` OpenSSH 8.4 o più recente.
Tutte le prove di RsyncGUI su Windows sono fatte con MSYS2.

## Funzionalità

- **Aggiungi / Rimuovi** profili di sincronizzazione, ciascuno con **più sorgenti e più destinazioni**: rsync accetta nativamente più sorgenti in un solo comando, ma una sola destinazione per invocazione, quindi l'app esegue un rsync per ogni destinazione, in sequenza.
- **Tag**: profili con lo stesso tag si avviano tutti insieme con un clic, **in sequenza, uno alla volta**, mai in parallelo.
- **Ordinamento** per tag (alfabetico) o manuale (drag & drop), con preferenza ricordata tra un avvio e l'altro.
- Opzioni rsync per profilo: checksum, elimina extra, simulazione, compressione, output dettagliato, scrittura diretta (`--inplace`) — sempre con `-a` per preservare permessi e timestamp.
- **Ricrea struttura** (per profilo): copiando `/A` in `/B` si ottiene `/B/A/…` invece di riversare il contenuto di `A` direttamente in `/B`. È la regola dello slash finale di rsync resa esplicita. L'interruttore è rosso quando è attivo perché **spegnerlo dopo che il profilo ha già copiato è distruttivo se è attivo anche `--delete`**: la cartella `/B/A` scritta in precedenza diventa "di troppo" e rsync la elimina. L'app chiede conferma prima di lasciarlo spegnere, spiegando cosa succederebbe.
- **Ripristina**: esegue la copia al contrario, dal backup verso la cartella originale. Disponibile solo sui profili con **una sorgente e una destinazione**, perché con più percorsi i file si mescolano nella destinazione e non è più possibile sapere da dove veniva ciascuno. Mostra sempre i percorsi esatti prima di procedere, tiene conto di "ricrea struttura" per sapere dove sta davvero il backup, e chiede ogni volta se applicare `--delete` — con una seconda conferma, perché in questa direzione cancellerebbe dall'originale tutto ciò che è stato creato dopo il backup.
- **Le copie proseguono a finestra chiusa** (dalla 2.3): una copia avviata non appartiene più alla finestra che l'ha lanciata. Chiudere l'app — o un suo crash — non la interrompe; alla riapertura la sezione **Attività** mostra cosa è ancora in corso e permette di seguirlo o interromperlo. Resta comunque **una sola copia alla volta**, come prima. Disattivabile dall'interruttore in Attività.
- **Cronologia governabile**: le copie concluse restano in Attività per 8 ore (regolabile lì stesso), poi vengono rimosse all'apertura dell'app — e con loro l'esito mostrato sui profili. Il pulsante "Pulisci cronologia" svuota subito tutto tranne le copie in corso.
- **rsync non incluso, ma guidato**: se all'avvio rsync non è nel PATH, l'app lo dice subito con i collegamenti per installarlo (GNU rsync, openrsync, Homebrew su macOS, cwRsync/MSYS2 su Windows). Funziona indifferentemente con rsync GNU e openrsync.
- **Barra di avanzamento** con percentuale e file trasferiti, sulla card del profilo e nella sezione Attività. La percentuale la calcola rsync stesso, che è l'unico a sapere quanti file toccherà davvero: un conteggio fatto in anticipo sarebbe smentito dal primo file saltato perché già aggiornato. Quando rsync non riporta nulla — una copia incrementale senza modifiche non emette progresso — la barra resta indeterminata invece di mostrare uno zero inventato.
- **Pulizia automatica dei log**: il log di una copia riuscita viene cancellato appena finisce, lasciando solo il riepilogo; si conservano quelli delle copie fallite (30 giorni), con un tetto complessivo sulla cartella. Un backup non deve riempire il disco su cui salva.
- **Notifiche di sistema** al termine di ogni profilo, con l'esito (completato, completato con avvisi, fallito). Un'interruzione volontaria non notifica nulla.
- **Esclusioni automatiche** dei file che i sistemi operativi creano da soli (`.DS_Store`, `Thumbs.db`, `desktop.ini`, ecc.), attive di default, più esclusioni personalizzate.
- **Backup su pCloud** (dalla 3.0): pCloud accetta rsync su SSH, porta 22 (`rsync.pcloud.com` per gli USA, `ersync.pcloud.com` per l'Europa; utente l'e-mail dell'account). Nell'editor, **+ pCloud…** chiede server, e-mail e cartella e salva la destinazione come una normale destinazione remota (`email@ersync.pcloud.com:/Backup/`). **La password non è mai nei profili, nell'esportazione, nel log o sulla riga di comando**: alla prima copia la finestra di accesso la verifica e, se lasci la casella «Ricorda», la affida al **portachiavi del sistema** (Keychain su macOS, Gestione credenziali su Windows, Secret Service — GNOME Keyring/KWallet — su Linux). Da allora le copie partono senza riaccedere; «Dimentica password» (nella finestra pCloud) la toglie. Senza portachiavi (una sessione Linux spoglia) la password vale per la sessione e l'app lo dice. Arriva a `ssh` tramite `SSH_ASKPASS` (il programma stesso fa da helper) e funziona anche con le copie a finestra chiusa. Le opzioni sono quelle del manuale di pCloud (niente permessi, proprietario, date, link simbolici, file speciali) più `--size-only`, perché senza date rsync non saprebbe riconoscere i file già copiati; se serve il confronto sicuro si attiva il checksum. Con la verifica in due passaggi pCloud manda l'e-mail «New login attempt» al primo accesso da un dispositivo nuovo: si approva una volta. La cartella Crypto di pCloud non è raggiungibile via rsync. Serve un `ssh` accanto a `rsync` (OpenSSH 7.6+; per `SSH_ASKPASS_REQUIRE`, 8.4+).
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

Prerequisiti: [Go](https://go.dev/dl/) nella versione richiesta da `go.mod` (attualmente 1.26+) e il CLI di Wails, fissato alla stessa versione usata in CI (evita disallineamenti fra macchine):

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
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
- **Linux**: `build-essential`, `pkg-config`, `libgtk-3-dev`, `libwebkit2gtk-4.1-dev` (su Ubuntu 24.04+ la `4.0-dev` non esiste più) — e compilare con `wails build -tags webkit2_41`, altrimenti pkg-config cerca comunque la 4.0.
- **Windows**: WebView2 (incluso in Windows 10/11). `rsync` non è nativo su Windows: serve nel `PATH` (MSYS2, cwRsync o WSL; istruzioni sopra, «Windows: installare rsync») — l'app lo segnala all'avvio se manca.

La compilazione va fatta sulla piattaforma di destinazione: il cross-compile con UI nativa non è affidabile con Wails. Per questo le release su GitHub sono compilate da [GitHub Actions](.github/workflows/build.yml) su runner macOS, Windows e Linux reali.

## Struttura del progetto

```
main.go              avvio Wails, opzioni finestra
app.go               backend: store JSON, import/export, runner rsync
app_test.go          test unitari
frontend/ui/         UI (HTML/CSS/JS vanilla, nessun build step)
assets/              icona sorgente e template Info.plist per Wails
scripts/             script di build e generazione icona
```

## Licenza

[MIT](LICENSE) © 2026 Trismegisto Open Source: software libero, si può usare,
copiare, modificare e ridistribuire. Le librerie Go incorporate nel binario e
le loro licenze (tutte permissive) sono elencate in
[THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md).

Contributi e segnalazioni: [CONTRIBUTING.md](CONTRIBUTING.md). Problemi di
sicurezza: [SECURITY.md](SECURITY.md).
