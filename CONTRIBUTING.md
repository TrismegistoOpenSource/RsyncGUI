# Contribuire a RsyncGUI

Grazie dell'interesse. RsyncGUI è software libero con licenza [MIT](LICENSE):
inviando un contributo accetti che venga distribuito con la stessa licenza.

## Segnalare un problema

Apri una *issue* dicendo: sistema operativo e versione di RsyncGUI, cosa hai
fatto, cosa ti aspettavi e cosa è successo.

## Proporre una modifica

1. Fai un *fork* e lavora su un ramo a parte.
2. Compila e prova sul tuo sistema (servono Go e Wails, vedi il
   [README](README.md)):

   ```bash
   bash scripts/ci-locale.sh
   ```

   Esegue build, `go vet`, `gofmt`, i test (con `-race`), il pacchetto e la sua
   verifica.
3. Le prove non devono mai toccare le impostazioni vere dell'utente (`profiles.json`): usano una cartella temporanea (`t.TempDir()`, variabile `RSYNCGUI_CONFIG_DIR`).
4. Ogni file di codice comincia con `SPDX-License-Identifier: MIT`.
5. Apri una *pull request* spiegando il perché della modifica.

## Dipendenze

Il binario incorpora Wails e i moduli che si porta dietro. Una nuova
dipendenza deve avere una licenza permissiva (MIT, BSD, ISC, Apache-2.0) e
comparire in [THIRD-PARTY-LICENSES.md](THIRD-PARTY-LICENSES.md).
