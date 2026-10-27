# Componenti di terze parti

RsyncGUI è distribuito sotto [licenza MIT](LICENSE). Il binario compilato incorpora
staticamente le librerie Go elencate qui sotto, ciascuna sotto la propria
licenza permissiva (tutte compatibili con la MIT). Le licenze sono state
verificate sui file `LICENSE` dei rispettivi moduli.

I testi di licenza viaggiano con l'app, nella cartella `LICENZE`: la licenza
MIT, questo file e i testi di **ogni modulo Go** compilato nel binario
(sottocartella `moduli-go/`, elenco completo in `moduli-go/ELENCO.txt`),
raccolti da `scripts/raccogli-licenze.sh` a ogni build. Si trova in
`Contents/Resources/LICENZE/` su macOS, accanto all'eseguibile nello zip per
Windows e in `usr/share/doc/rsyncgui/` nell'AppImage per Linux.

## Dipendenze dirette

| Componente | Licenza | Copyright / origine |
|---|---|---|
| [Wails v2](https://wails.io) | MIT | © Lea Anthony |
| [google/uuid](https://github.com/google/uuid) | BSD-3-Clause | © 2009, 2014 Google Inc. |
| [gen2brain/beeep](https://github.com/gen2brain/beeep) | BSD-2-Clause | © 2017 Milan Nikolic |
| [zalando/go-keyring](https://github.com/zalando/go-keyring) | MIT | © 2018 Zalando SE (portachiavi del sistema, per la password di pCloud) |
| [danieljoos/wincred](https://github.com/danieljoos/wincred) | MIT | © Daniel Joos (portato da go-keyring, solo Windows) |
| Go standard library e runtime | BSD-3-Clause | © The Go Authors |

## Dipendenze indirette (portate da Wails)

| Componente | Licenza |
|---|---|
| labstack/echo, labstack/gommon | MIT |
| gorilla/websocket | BSD-2-Clause |
| go-ole/go-ole | MIT |
| godbus/dbus | BSD-2-Clause |
| jchv/go-winloader | ISC |
| leaanthony/go-ansi-parser, gosod, slicer, u | MIT |
| mattn/go-colorable, go-isatty | MIT |
| pkg/browser, pkg/errors | BSD-2-Clause |
| rivo/uniseg | MIT |
| samber/lo | MIT |
| tkrajina/go-reflector | Apache-2.0 |
| valyala/bytebufferpool, fasttemplate | MIT |
| wailsapp/go-webview2, wailsapp/mimetype | MIT |
| bep/debounce | MIT |
| jackmordaunt/go-toast | Unlicense **oppure** MIT |
| golang.org/x/* (crypto, net, sys, text) | BSD-3-Clause |

## Dipendenze indirette (portate da beeep, le notifiche)

| Componente | Licenza |
|---|---|
| esiqveland/notify | BSD-3-Clause |
| jackmordaunt/icns | MIT |
| nfnt/resize | ISC |
| sergeymakinen/go-bmp, go-ico | BSD-3-Clause |
| tadvi/systray | MIT |
| jackmordaunt/go-toast (v1, come la v2 sopra) | Unlicense **oppure** MIT |

## Codice di altri autori dentro i moduli (licenze annidate)

Alcuni moduli contengono, in una sottocartella, pacchetti di altri autori con
una licenza propria. Finiscono nel binario, quindi i loro testi viaggiano con
l'app in `LICENZE/moduli-go/<modulo>/annidate/` (li raccoglie
`scripts/raccogli-licenze.sh`, e `scripts/verifica-pacchetto.sh` controlla che
ci siano):

| Pacchetto | Licenza | Copyright | Dove finisce |
|---|---|---|---|
| `wails/v2/internal/frontend/desktop/windows/winc` | MIT | © 2019 winc Authors | solo Windows |
| `wails/v2/internal/go-common-file-dialog` | MIT | © 2019 Harry Phillips | solo Windows |
| `go-webview2/webviewloader` | ISC | © 2020 John Chadwick, © 2022 Wails Project Developers | solo Windows |
| `wails/v2/internal/typescriptify` | Apache-2.0 | © 2015 Tomo Krajina | tutti i sistemi |

## Componenti di sistema non ridistribuiti

- **rsync** — l'app lo invoca come programma esterno già presente sul sistema
  (o installato dall'utente); non è incluso né ridistribuito. rsync è GPL-3.0:
  RsyncGUI lo lancia come processo separato e non lo collega né lo include,
  quindi la sua licenza non si estende a questa app.
- **WebView di sistema** — la UI usa la webview dell'OS (WebKit su macOS,
  WebView2 su Windows, WebKitGTK su Linux): componenti del sistema operativo,
  non ridistribuiti.

## Frontend

HTML/CSS/JavaScript scritti in proprio, nessuna libreria di terze parti.
