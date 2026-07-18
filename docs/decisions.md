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

**Corretto nella 2.3.2**: la 2.3.0 *cancellava* il log di una copia riuscita, non lo
riduceva. L'effetto pratico era che finita una copia non si poteva più guardare cosa
avesse fatto — e la prima cosa che si vuole fare quando un backup finisce è proprio
quella. L'obiettivo della ritenzione è che la cartella non cresca senza limite, non
distruggere le prove. Ora di una copia riuscita resta la **coda** del log (64 KB):
cinquanta job di storia fanno pochi megabyte, contro le centinaia che una singola
esecuzione verbosa può produrre. Quello che è fallito conserva il log intero, perché
è lì che sta la risposta.

---

## 5-bis. Lo stato dei profili si ricava dai job su disco (2.3.2)

**Deciso**: il pallino di stato su ogni profilo, e il fatto che l'app sia occupata, si
ricavano dai file dei job, non da eventi emessi durante l'esecuzione.

**Perché**: fino alla 2.2 lo stato arrivava da eventi `run:status` emessi dal runner
mentre lavorava dentro la finestra. Col detach non c'è più niente da emettere — la
copia può benissimo essere stata avviata da una finestra che non esiste più — e infatti
nella 2.3.0 i pallini erano rimasti spenti, i pulsanti "Avvia" non si disabilitavano
più durante una copia e "Interrompi" non compariva. Leggendo dai job si recupera tutto,
e in più **lo stato sopravvive alla chiusura dell'app**: riaprendola si vede ancora com'è
andata l'ultima copia di ogni profilo, cosa che prima si perdeva.

**Conseguenza**: ogni job registra quali profili ha toccato (`profileIds`), e il job più
recente che nomina un profilo è quello che ne descrive lo stato.

---

## 5-ter. La percentuale la calcola rsync, non noi (2.4)

**Deciso**: l'avanzamento si ricava analizzando l'output di `--progress`, non
contando i file prima di partire.

**Perché**: è rsync a decidere cosa trasferirà davvero, saltando ciò che è già
aggiornato. Qualsiasi conteggio fatto da noi in anticipo verrebbe smentito dal primo
file saltato, e su un backup incrementale — il caso normale — sarebbe sbagliato di
ordini di grandezza.

**Scartato `--info=progress2`**, che sarebbe più ordinato: **non esiste ovunque**. macOS
(da Sequoia) monta **openrsync**, che si dichiara "rsync version 2.6.9 compatible" e
rifiuta l'opzione. `--progress` funziona su entrambe le famiglie.

**Trappola verificata sul campo**: le due famiglie contano in direzioni opposte.
GNU rsync scrive `to-chk=N/M` dove N sono i file **rimanenti** e scende verso zero;
openrsync scrive `to-check=N/M` dove N sono quelli **fatti** e sale verso il totale.
Nessuno dei due dichiara quale sia, quindi la direzione si deduce guardando muovere le
prime due letture: finché non è nota la percentuale resta sconosciuta, che è meglio di
mostrarne una invertita. (Un primo tentativo con l'espressione regolare
`to-chk(?:eck)?` sembrava coprire entrambe le grafie ma accetta "to-chk" e "to-chkeck",
non "to-check": lo hanno scoperto i test scritti sui formati veri catturati dai due
programmi.)

**Le righe di progresso non finiscono nel log**: sono stato transitorio, non eventi, e
GNU rsync le riscrive in continuazione con un ritorno a capo mentre un file grande
attraversa. Conservarne ogni revisione seppellirebbe il log sotto il proprio contatore.

**La percentuale può mancare**: una copia incrementale senza modifiche non emette
progresso affatto (verificato). In quel caso la barra resta indeterminata invece di
inventare uno zero.

---

## 5-quater. Lo stop attraversa i processi come FILE, non come segnale (2.5)

**Deciso**: "Interrompi" su un job staccato scrive `<jobid>.stop`; il supervisore lo
sonda ogni 300 ms. Su Unix parte anche un SIGINT, ma come acceleratore: se manca,
il file basta.

**Perché**: il meccanismo a segnali era **rotto su Windows senza che nessun test
potesse dirlo da macOS**. Il supervisore è un `DETACHED_PROCESS`: non ha console, e
`GenerateConsoleCtrlEvent` — l'unico surrogato di SIGINT — viaggia solo dentro una
console condivisa. In più un segnale è indirizzato a un *pid*, cioè a un nome
riciclabile; il file è indirizzato al *job*. Stessa lezione del lock (§3): mai
fidarsi del pid.

---

## 5-quinquies. Cronologia: ritenzione scelta dall'utente + pulizia manuale (2.5)

**Deciso**: i job conclusi restano in Attività per un numero di ore scelto
dall'utente (default 8, impostabile in Attività), applicato **all'apertura
dell'app**; un pulsante "Pulisci cronologia" li rimuove subito. I limiti di
sicurezza di Cleanup (tetto sulla cartella, ecc.) restano sotto come rete.

**Perché**: dalla 2.3.2 l'esito sui profili deriva dai job su disco, quindi un job
che non sparisce mai significa un led che non torna mai grigio — segnalato
dall'utente come difetto. La ritenzione utente vive nella finestra e non nel
supervisore, perché il supervisore non ha titolo per leggere le preferenze della
finestra.

---

## 5-sexies. Il gate "una cosa alla volta" copre anche la verifica (2.5)

**Deciso**: "Verifica cartella" prende lo stesso lock globale dei job
(`running.lock`) per tutta la sua durata; e l'avvio di un job staccato rifiuta se la
finestra è occupata da una verifica.

**Perché**: la 2.3 aveva spostato l'invariante su file per le copie, ma la verifica
era rimasta sul flag in memoria: una verifica poteva girare mentre una copia
staccata scriveva, e viceversa. Buco trovato in audit, non da un sintomo — la
classe di errore era già nota (§1) e andava cercata ovunque il flag in memoria
fosse rimasto l'unica guardia.

---

## 5-septies. Link ai siti di rsync: nessun obbligo di licenza (2.5)

**Deciso**: l'app mostra collegamenti per scaricare rsync/openrsync (popup
all'avvio se mancante, voce in Attività). L'installazione resta un atto
dell'utente.

**Perché nessun cambiamento alle licenze**: un collegamento ipertestuale non
distribuisce né incorpora nulla — GPL e ISC pongono obblighi su *distribuzione* e
*derivazione*, non sul rimandare al sito del produttore. THIRD-PARTY-LICENSES
continua a citare solo ciò che il binario incorpora. Le URL sono in una whitelist
nel backend: un metodo esposto che aprisse URL arbitrarie consegnerebbe la
navigazione del browser a qualunque cosa giri nella webview.

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

**Corollario scoperto nella 2.3.1**: non conta solo la *frequenza* di ciò che attraversa
il ponte Go↔webview, ma anche la sua *dimensione*. Il risultato di un metodo esposto
viene consegnato al webview sul thread dell'interfaccia della piattaforma, quindi un
blocco grosso non costa soltanto tempo: **blocca la finestra stessa**, che smette di
rispondere a trascinamenti e clic. Sembra un'interfaccia lenta, ma l'interfaccia è
ferma — è il ponte a essere occupato.

Concretamente: "Segui" ripartiva da inizio log, quindi agganciare un job che aveva già
scritto 14 MB significava ritrasmetterli tutti a 512 KB per ciclo, ~28 cicli di ponte
saturo. Ora si parte dalla coda e si legge al massimo 64 KB per volta: 127 KB totali al
posto di 14 MB. Seguire un log dal vivo non ha bisogno di volume — nessuno legge mezzo
megabyte al secondo, serve solo stare vicino alla fine.

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
