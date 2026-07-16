# Componenti di terze parti

RsyncGUI è distribuito sotto [GPL-3.0](LICENSE). Il binario compilato incorpora
staticamente le librerie Go elencate qui sotto, ciascuna sotto la propria
licenza permissiva (tutte compatibili con la GPL-3.0). Le licenze sono state
verificate sui file `LICENSE` dei rispettivi moduli.

## Dipendenze dirette

| Componente | Licenza | Copyright / origine |
|---|---|---|
| [Wails v2](https://wails.io) | MIT | © Lea Anthony |
| [google/uuid](https://github.com/google/uuid) | BSD-3-Clause | © 2009, 2014 Google Inc. |
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

## Componenti di sistema non ridistribuiti

- **rsync** — l'app lo invoca come programma esterno già presente sul sistema
  (o installato dall'utente); non è incluso né ridistribuito. rsync è GPL-3.0,
  la stessa licenza di questa app.
- **WebView di sistema** — la UI usa la webview dell'OS (WebKit su macOS,
  WebView2 su Windows, WebKitGTK su Linux): componenti del sistema operativo,
  non ridistribuiti.

## Frontend

HTML/CSS/JavaScript scritti in proprio, nessuna libreria di terze parti.
