# Decisioni di progetto

Perché RsyncGUI è fatta così. Ogni voce dice **cosa** è stato deciso, **perché**, e
**cosa è stato scartato** — la parte più utile, perché è quella che di solito qualcuno
ripropone in buona fede due anni dopo.

Le voci non si cancellano: se una decisione cambia, si aggiunge una voce nuova che
sostituisce la vecchia, indicandolo.

---

## 1. Un solo job alla volta, sempre

**Deciso**: RsyncGUI esegue una sola copia per volta. I profili di uno stesso tag
girano in sequenza, le destinazioni di un profilo pure, e anche "Verifica cartella"
condivide lo stesso lucchetto. Dalla 2.3 l'invariante è imposta da un lock su file
(`jobs/running.lock`) e non più da un flag in memoria.

**Perché**: più rsync in parallelo sullo stesso disco o sulla stessa rete si rubano
banda e fanno saltare le testine avanti e indietro; il risultato è che finiscono tutti
più tardi di quanto avrebbero fatto in fila. In più i log si intreccerebbero
diventando illeggibili, e due copie potrebbero ritrovarsi a scrivere sulla stessa
destinazione.

**Perché un lock su file e non il flag in memoria**: dalla 2.3 i job sopravvivono alla
chiusura della finestra. Un flag in memoria muore con la finestra, quindi basterebbe
chiudere e riaprire durante una copia per riuscire ad avviarne una seconda.

**Scartato**: un lock per profilo. Permetterebbe a due profili diversi di girare
insieme, cambiando in silenzio un comportamento storico. Se un giorno si vorrà il
parallelismo dovrà essere una scelta esplicita dell'utente, mai un effetto collaterale
— e dovrà arrivare con un controllo che due job non scrivano sulla stessa
destinazione, che è il vero pericolo lì.

---

## 2. I job sopravvivono alla chiusura della finestra (2.3)

**Deciso**: una copia è eseguita da un **supervisore**, cioè lo stesso binario
reinvocato con `--supervise`, staccato dalla finestra. Stato e log vivono in
`<config>/jobs/`, un file per job, con nomi derivati dal `jobId`.

**Perché un supervisore e non rsync direttamente**: se si staccasse rsync e la
finestra morisse, nessuno raccoglierebbe il suo codice d'uscita — verrebbe raccolto da
init e perso. Alla riapertura si saprebbe che il job non c'è più, ma non se è andato
bene. Il supervisore è il pezzo che aspetta rsync e registra come è finito.

**La causa vera del problema che risolve**: fino alla 2.2 una copia moriva con la
finestra non per mancanza di detach, ma per la **pipe**. rsync scriveva l'output su una
`io.Pipe` letta dalla GUI; alla morte della GUI il capo di lettura si chiudeva e rsync
moriva alla prima scrittura. Verificato: stesso rsync e stesso `kill -9` sul padre,
output su pipe → muore, output su file → sopravvive e finisce.

**Scartato: integrare tmux.** La licenza non era l'ostacolo (tmux è ISC, permissiva).
Ma su Windows non esiste (solo WSL o port separati), sarebbe una dipendenza esterna da
far installare, e costringerebbe l'app a interpretare l'output di un altro programma
per sapere lo stato dei job. Tutto quello che serviva si ottiene nativamente in Go,
uguale sulle tre piattaforme.

---

## 3. Il lock è la verità, non il pid

**Deciso**: per sapere se un job è ancora vivo si guarda se il suo file di lock è
tenuto, non se il pid registrato esiste.

**Perché**: i pid vengono riciclati. Uno stato che dice "pid 12064 in esecuzione" può
riferirsi, due giorni dopo, a un programma qualunque: il job risulterebbe in corso per
sempre. Il lock invece viene rilasciato dal sistema operativo appena chi lo teneva
muore, in qualunque modo muoia — uscita pulita, crash o mancanza di corrente.

**Conseguenza**: un job con stato `running` e lock libero non è "in corso" né
"fallito": è `orphaned`, il supervisore è morto senza registrare un esito. Dirlo è più
onesto che inventare un successo o un fallimento.

---

## 4. Nessun percorso assoluto nei file di stato

**Deciso**: i file di un job (`.json`, `.log`, `.lock`) si **ricavano** dal `jobId` più
la cartella di configurazione corrente. Non si memorizza né si legge mai un percorso.

**Perché**: un percorso assoluto memorizzato è fragile e insicuro insieme. Fragile
perché basta spostare la cartella di configurazione, cambiare `RSYNCGUI_CONFIG_DIR` o
rinominare la home perché punti nel vuoto. Insicuro perché la pulizia dei log
**cancella file**: un file di stato manomesso potrebbe farle cancellare qualcosa
altrove. Il `jobId` è validato contro un'espressione regolare stretta prima di
comporre qualsiasi percorso.

---

## 5. Ritenzione: i log delle copie riuscite si cancellano subito

**Deciso**, su tre livelli:

1. **Durante il job**: il log è limitato (testa + coda, il centro viene omesso con un
   avviso visibile). Un `rsync -v` su milioni di file scrive gigabyte.
2. **A fine job**: il log di una copia **riuscita o interrotta volontariamente viene
   cancellato subito**; resta il `summary` di una riga nello stato. Si conservano solo
   i log di ciò che è fallito o è andato a metà.
3. **Periodicamente** (all'apertura e dopo ogni job): log falliti oltre 30 giorni,
   stati oltre 90, massimo 50 job in storia, tetto complessivo di 200 MB sulla
   cartella.

**Perché**: un backup che riempie il disco su cui salva ha vanificato sé stesso. E una
copia andata bene non ha niente da raccontare: il riepilogo occupa qualche centinaio di
byte invece di megabyte di righe "file trasferito".

**Guardia obbligatoria**: la pulizia non tocca mai un job il cui lock è occupato.
Cancellare il log di una copia in corso sarebbe il modo più stupido di rompere questa
funzione, e c'è un test apposta.

---

## 6. "Segui", non "Riprendi"

**Deciso**: il pulsante su un job vivo si chiama **Segui** e si limita a riagganciare
la vista. Non esiste un "Riprendi".

**Perché**: sarebbe ambiguo e confonderebbe due cose diverse. Riagganciarsi a un job
ancora vivo è una cosa; rilanciare un job interrotto è un'altra, ed è già **Avvia** —
**rsync è di per sé la ripartenza**, essendo incrementale riprende da dov'era. Un
pulsante "Riprendi" accanto ad "Avvia" farebbe pensare a due comportamenti diversi che
non esistono.

---

## 7. Niente rsync incorporato nell'app

**Deciso**: RsyncGUI resta una GUI sopra l'rsync installato nel sistema. Su Windows,
dove non c'è di serie, l'utente lo installa.

**Perché non è un problema di licenza**: rsync è GPLv3 e RsyncGUI pure, quindi
compatibili; e comunque lo invochiamo come processo separato, non lo linkiamo.

**Perché si è deciso di no lo stesso**:
- Distribuire binari GPLv3 obbliga a fornire il **sorgente corrispondente**, ad ogni
  release e per ogni aggiornamento di sicurezza. Un rsync incorporato diventa roba
  nostra da mantenere; uno di sistema lo aggiorna il gestore pacchetti.
- Su Windows rsync gira sopra Cygwin e **non capisce i percorsi Windows**: vuole
  `/cygdrive/c/…`, e in sintassi rsync i due punti significano "host remoto", quindi
  `C:\Backup` verrebbe letto come *l'host chiamato C*. Servirebbe tradurre tutti i
  percorsi, UNC compresi. Era il costo vero, ben più della licenza.

**Scartato**: `openrsync` (licenza BSD, quello che Apple usa da macOS Sequoia). Non ha
un porting Windows, quindi non risolve il problema che avrebbe dovuto risolvere.

---

## 8. Il frontend resta JavaScript senza passo di build

**Deciso**: niente TypeScript, niente npm, niente bundler. `frontend:install` e
`frontend:build` restano vuoti in `wails.json`.

**Perché**: zero dipendenze a runtime, nessuna catena di fornitura da sorvegliare,
l'app funziona completamente offline e la CI resta semplice. Una migrazione a `.ts`
porterebbe npm, un compilatore e tre job di CI da rifare.

**Via di mezzo prevista** (non ancora fatta): `// @ts-check` più un `jsconfig.json` e
`tsc --noEmit` come controllo in CI. Runtime identico, ma refusi, arità sbagliate e
valori nulli verrebbero segnalati. Da fare come passo isolato, mai insieme a una
funzione nuova: cambiare linguaggio mentre si aggiunge comportamento rende impossibile
attribuire un bug all'uno o all'altro.

---

## 9. Mai un evento per riga verso l'interfaccia

**Deciso**: l'output di rsync viene accorpato in blocchi lato Go e scritto nel DOM una
volta per frame lato JavaScript.

**Perché**: `rsync -v` emette migliaia di righe al secondo (misurate ~3200/s su disco
locale). Un evento per riga satura il thread dell'interfaccia. Peggio ancora,
`element.textContent += testo` è **quadratico**: rilegge e ricostruisce l'intero log ad
ogni riga. Misurato su 20.000 righe: 2527 ms contro 2 ms della versione a blocchi, cioè
circa 1700 volte più lento. Insieme, bloccavano la finestra per tutta la durata della
copia.

**Regola generale che se ne ricava**: mai `textContent +=` in un ciclo, e mai un evento
per riga verso un webview.

---

## 10. profiles.json è sacro

**Deciso**: la configurazione dell'utente si tocca il meno possibile. Le preferenze
dell'app stanno in un file separato (`settings.json`), i job in una sottocartella
(`jobs/`), e prima di ogni salvataggio viene tenuta **una** copia del contenuto
precedente in `profiles.json.bak`.

**Perché**: è l'unica cosa in tutta l'applicazione che sarebbe davvero doloroso
perdere, e ogni motivo in più per riscriverla è un'occasione in più per danneggiarla.
Le preferenze invece si riconfigurano in un minuto, quindi vivono altrove.

**Conseguenza sui test**: ogni test punta `RSYNCGUI_CONFIG_DIR` a una cartella
temporanea. Sempre, senza eccezioni.
