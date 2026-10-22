# Sicurezza

## Segnalare una vulnerabilità

Non aprire una *issue* pubblica: usa la segnalazione privata di GitHub
(scheda **Security** del repository → *Report a vulnerability*).

## Cosa sapere

RsyncGUI lancia `rsync` con le opzioni dei profili salvati dall'utente e non invia dati a nessun server: tutto resta sul computer. L'unica connessione che apre da sé è l'**autoverifica all'avvio**: una volta per versione scarica tre file pubblici della release (somme di controllo, firma ed elenco dei file) per controllare di essere integro e firmato da Trismegisto Open Source; non parte nessun dato. Limiti e dettagli in [docs/autoverifica.md](docs/autoverifica.md). Le impostazioni stanno in `profiles.json`, nella cartella di configurazione dell'utente.
