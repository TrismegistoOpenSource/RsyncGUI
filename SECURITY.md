# Sicurezza

## Segnalare una vulnerabilità

Non aprire una *issue* pubblica: usa la segnalazione privata di GitHub
(scheda **Security** del repository → *Report a vulnerability*).

## Cosa sapere

RsyncGUI lancia `rsync` con le opzioni dei profili salvati dall'utente e non invia dati a nessun server: tutto resta sul computer. Le impostazioni stanno in `profiles.json`, nella cartella di configurazione dell'utente.
