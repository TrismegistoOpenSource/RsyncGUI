// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// Nessun test tocca il portachiavi vero dell'utente.
func init() { keyring.MockInit() }

func TestPCloudAccount(t *testing.T) {
	cases := []struct {
		spec, email, host string
		ok                bool
	}{
		{"mario@example.com@rsync.pcloud.com:/Backup/", "mario@example.com", "rsync.pcloud.com", true},
		{"mario@example.com@ersync.pcloud.com:/", "mario@example.com", "ersync.pcloud.com", true},
		{`"mario@example.com"@rsync.pcloud.com:/x`, "mario@example.com", "rsync.pcloud.com", true},
		{"mario@example.com@RSYNC.pcloud.com:/x", "mario@example.com", "rsync.pcloud.com", true},
		{"utente@nas.local:/backup", "", "", false},
		{"rsync.pcloud.com:/x", "", "", false}, // niente utente
		{"/Volumes/pCloud/Backup", "", "", false},
		{`C:\Users\me`, "", "", false},
		{"rsync://rsync.pcloud.com/x", "", "", false},
	}
	for _, c := range cases {
		email, host, ok := pcloudAccount(c.spec)
		if ok != c.ok || email != c.email || host != c.host {
			t.Errorf("pcloudAccount(%q) = %q, %q, %v", c.spec, email, host, ok)
		}
	}
}

func TestPCloudSpecAndNormalize(t *testing.T) {
	if got := pcloudSpec("eu", " mario@example.com ", "Backup/Foto"); got != "mario@example.com@ersync.pcloud.com:/Backup/Foto" {
		t.Errorf("EU: %q", got)
	}
	if got := pcloudSpec("us", "mario@example.com", "/"); got != "mario@example.com@rsync.pcloud.com:/" {
		t.Errorf("US: %q", got)
	}
	if got := pcloudNormalize(`"mario@example.com"@rsync.pcloud.com:/x`); got != "mario@example.com@rsync.pcloud.com:/x" {
		t.Errorf("virgolette non tolte: %q", got)
	}
	// scritto a mano con le maiuscole: fino alla 3.0.0 mandava in crash l'app
	if got := pcloudNormalize("mario@example.com@Rsync.PCloud.com:/Backup/Foto"); got != "mario@example.com@rsync.pcloud.com:/Backup/Foto" {
		t.Errorf("server in maiuscolo: %q", got)
	}
	if got := pcloudNormalize("/tmp/x"); got != "/tmp/x" {
		t.Errorf("un percorso locale non va toccato: %q", got)
	}
}

func TestRsyncArgsPCloud(t *testing.T) {
	dest := "mario@example.com@ersync.pcloud.com:/Backup/"
	args := rsyncArgs(SyncOptions{}, []string{"/src"}, dest, "")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--no-times", "--no-links", "--no-specials", "--no-devices", "--no-perms", "--no-owner", "--no-group", "--size-only", "StrictHostKeyChecking=accept-new"} {
		if !strings.Contains(joined, want) {
			t.Errorf("manca %q in %v", want, args)
		}
	}
	// -a viene prima delle negazioni: l'ultima parola è quella che vale.
	if args[0] != "-a" || strings.Index(joined, "--no-times") < strings.Index(joined, "-a ") {
		t.Errorf("le negazioni devono seguire -a: %v", args)
	}
	if args[len(args)-1] != dest {
		t.Errorf("destinazione: %q", args[len(args)-1])
	}
	if strings.Contains(joined, "SYNC") || strings.Contains(joined, "PASSWORD") {
		t.Errorf("niente segreti nella riga di comando: %v", args)
	}

	// Con il checksum richiesto non si ripiega su --size-only.
	if cs := strings.Join(rsyncArgs(SyncOptions{Checksum: true}, []string{"/src"}, dest, ""), " "); strings.Contains(cs, "--size-only") {
		t.Error("con -c niente --size-only")
	}
	// Senza pCloud, nulla cambia.
	if loc := strings.Join(rsyncArgs(SyncOptions{}, []string{"/src"}, "/dst", ""), " "); strings.Contains(loc, "--no-times") || strings.Contains(loc, "ssh") {
		t.Errorf("copia locale alterata: %s", loc)
	}
	// Anche come sorgente (ripristino).
	if rs := strings.Join(rsyncArgs(SyncOptions{}, []string{dest}, "/dst", ""), " "); !strings.Contains(rs, "--no-times") {
		t.Errorf("ripristino da pCloud senza le opzioni pCloud: %s", rs)
	}
}

func TestPCloudChildEnv(t *testing.T) {
	d1 := "a@example.com@rsync.pcloud.com:/x"
	d2 := "b@example.com@rsync.pcloud.com:/x"
	secrets := map[string]string{"a@example.com": "segreto"}

	if env, err := pcloudChildEnv(secrets, []string{"/src"}, "/dst"); env != nil || err != nil {
		t.Errorf("copia locale: env=%v err=%v", env, err)
	}
	env, err := pcloudChildEnv(secrets, []string{"/src"}, d1)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"SSH_ASKPASS_REQUIRE=force", askpassPassEnv + "=segreto", "SSH_ASKPASS="} {
		if !strings.Contains(joined, want) {
			t.Errorf("manca %q", want)
		}
	}
	if _, err := pcloudChildEnv(secrets, []string{"/src"}, d2); err == nil || !strings.HasPrefix(err.Error(), loginPrefix) {
		t.Errorf("senza password serve l'accesso: %v", err)
	}
	if _, err := pcloudChildEnv(map[string]string{"a@example.com": "x", "b@example.com": "y"}, []string{d1}, d2); err == nil || strings.HasPrefix(err.Error(), loginPrefix) {
		t.Errorf("due account insieme vanno rifiutati: %v", err)
	}
}

func TestPCloudNeedsLoginAndSecrets(t *testing.T) {
	a := NewApp()
	list := []SyncProfile{{Sources: []string{"/src"}, Destinations: []string{"/dst", "a@example.com@ersync.pcloud.com:/x"}}}
	if who := a.pcloudNeedsLogin(list); who != "a@example.com@ersync.pcloud.com" {
		t.Errorf("manca la password: %q", who)
	}
	a.pcloud = map[string]string{"a@example.com": "pw"}
	if who := a.pcloudNeedsLogin(list); who != "" {
		t.Errorf("password presente ma chiede ancora: %q", who)
	}
	if s := a.pcloudSecretsFor(list); s["a@example.com"] != "pw" || len(s) != 1 {
		t.Errorf("segreti: %v", s)
	}
	if err := a.PCloudForget("a@example.com"); err != nil {
		t.Fatal(err)
	}
	if who := a.pcloudNeedsLogin(list); who == "" {
		t.Error("dopo «dimentica» la password va richiesta di nuovo")
	}
	// La password non finisce mai nel profilo serializzato.
	a.pcloud = map[string]string{"a@example.com": "pw-segretissima"}
	raw, _ := list[0].MarshalJSON()
	if strings.Contains(string(raw), "segretissima") {
		t.Error("password nel JSON del profilo")
	}
}

func TestEnqueueAsksForLoginBeforeStarting(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())
	a := NewApp()
	err := a.enqueue([]SyncProfile{{Name: "p", Sources: []string{"/src"}, Destinations: []string{"a@example.com@rsync.pcloud.com:/x"}}})
	if err == nil || err.Error() != loginPrefix+"a@example.com@rsync.pcloud.com" {
		t.Fatalf("atteso l'invito all'accesso, ottenuto %v", err)
	}
	if a.busy {
		t.Error("nulla doveva partire")
	}
}

func TestAskpassAnswersPasswordOrYes(t *testing.T) {
	run := func(prompt string) string {
		t.Helper()
		old, oldArgs := os.Stdout, os.Args
		r, w, _ := os.Pipe()
		os.Stdout, os.Args = w, []string{"RsyncGUI", prompt}
		t.Setenv(askpassPassEnv, "segreto")
		ok := runAskpassIfRequested()
		w.Close()
		os.Stdout, os.Args = old, oldArgs
		out, _ := io.ReadAll(r)
		if !ok {
			t.Fatal("l'helper non si è attivato")
		}
		return strings.TrimSpace(string(out))
	}
	if got := run("mario@rsync.pcloud.com's password: "); got != "segreto" {
		t.Errorf("password: %q", got)
	}
	if got := run("Are you sure you want to continue connecting (yes/no/[fingerprint])? "); got != "yes" {
		t.Errorf("domanda sull'host: %q", got)
	}
	os.Unsetenv(askpassPassEnv)
	if runAskpassIfRequested() {
		t.Error("senza la variabile non deve attivarsi (aprirebbe la finestra)")
	}
}

// Un rsync vero con un finto ssh: controlla che la password arrivi a ssh solo
// attraverso l'ambiente dell'helper, che l'utente sia l'e-mail intera e che
// il hint compaia quando l'accesso fallisce.
func TestExecRsyncPCloudWithFakeSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("finto ssh in sh")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "visto.txt")
	fake := filepath.Join(dir, "fakessh")
	script := "#!/bin/sh\n{ echo \"ARGS: $*\"; echo \"PW=$" + askpassPassEnv + "\"; echo \"REQ=$SSH_ASKPASS_REQUIRE\"; } > '" + out + "'\nexit 255\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sshEnv, fake)

	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)

	a := NewApp()
	a.pcloud = map[string]string{"mario@example.com": "segreto"}
	code, err := a.execRsync(t.Context(), SyncOptions{}, []string{src}, "mario@example.com@ersync.pcloud.com:/Backup/")
	if err != nil {
		t.Fatal(err)
	}
	// ssh che muore subito: rsync dice 255 o 12 secondo chi se ne accorge prima.
	if code != 255 && code != 12 {
		t.Errorf("exit atteso 255 o 12, ottenuto %d", code)
	}
	if h := pcloudHint(code, []string{src}, "mario@example.com@ersync.pcloud.com:/Backup/"); h == "" {
		t.Error("manca il suggerimento sull'accesso")
	}
	got, _ := os.ReadFile(out)
	text := string(got)
	for _, want := range []string{"ersync.pcloud.com", "StrictHostKeyChecking=accept-new", "PW=segreto", "REQ=force"} {
		if !strings.Contains(text, want) {
			t.Errorf("ssh non ha visto %q:\n%s", want, text)
		}
	}
	// L'utente è l'e-mail intera. rsync GNU la passa a ssh con "-l utente host";
	// openrsync (macOS) lascia "utente@host" intero e ssh divide all'ultima "@".
	if !strings.Contains(text, "-l mario@example.com") && !strings.Contains(text, "mario@example.com@ersync.pcloud.com") {
		t.Errorf("ssh non ha ricevuto l'e-mail intera come utente:\n%s", text)
	}
	if strings.Contains(strings.SplitN(text, "PW=", 2)[0], "segreto") {
		t.Error("la password è finita fra gli argomenti di ssh")
	}
}

// Le password passano dalla finestra al supervisore nell'ambiente del processo
// e il supervisore le toglie subito: né rsync né ssh le ereditano per questa via.
func TestSecretsCarrierRoundTrip(t *testing.T) {
	cmd := exec.Command("true")
	if err := withSecretsCarrier(cmd, map[string]string{"a@example.com": "segreto"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, pcloudSecretsEnv+"=") {
			raw = strings.TrimPrefix(kv, pcloudSecretsEnv+"=")
		}
	}
	if raw == "" {
		t.Fatal("password non passate al supervisore")
	}
	t.Setenv(pcloudSecretsEnv, raw)
	got := takeSecretsCarrier()
	if got["a@example.com"] != "segreto" {
		t.Errorf("lette: %v", got)
	}
	if _, still := os.LookupEnv(pcloudSecretsEnv); still {
		t.Error("la variabile deve sparire dopo la lettura")
	}
	env, err := pcloudChildEnv(got, nil, "a@example.com@rsync.pcloud.com:/x")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, pcloudSecretsEnv+"=") {
			t.Error("il corriere delle password è arrivato a rsync")
		}
	}
	// Senza password niente variabile.
	empty := exec.Command("true")
	if err := withSecretsCarrier(empty, nil); err != nil || empty.Env != nil {
		t.Errorf("env=%v err=%v", empty.Env, err)
	}
}

// La password salvata nel portachiavi basta a una nuova sessione (un nuovo App):
// niente accesso, niente 2FA da rifare. E non finisce mai nel JSON.
func TestPCloudPasswordSurvivesRestartViaKeyring(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(keyringService, "a@example.com", "dal-portachiavi"); err != nil {
		t.Fatal(err)
	}
	list := []SyncProfile{{Sources: []string{"/src"}, Destinations: []string{"a@example.com@rsync.pcloud.com:/x"}}}

	a := NewApp() // sessione nuova: memoria vuota
	if who := a.pcloudNeedsLogin(list); who != "" {
		t.Fatalf("la password è nel portachiavi ma chiede l'accesso: %q", who)
	}
	if s := a.pcloudSecretsFor(list); s["a@example.com"] != "dal-portachiavi" {
		t.Errorf("segreti: %v", s)
	}

	// «Dimentica» la toglie anche dal portachiavi.
	if err := a.PCloudForget("a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get(keyringService, "a@example.com"); err == nil {
		t.Error("la password è rimasta nel portachiavi")
	}
	if err := a.PCloudForget("a@example.com"); err != nil {
		t.Errorf("dimenticare due volte non è un errore: %v", err)
	}
	if who := NewApp().pcloudNeedsLogin(list); who == "" {
		t.Error("dopo «dimentica» deve chiedere di nuovo")
	}
}

func TestPCloudLoginRefusesBadInput(t *testing.T) {
	a := NewApp()
	if _, err := a.PCloudLogin("a@example.com@rsync.pcloud.com", "", true); err == nil {
		t.Error("password vuota accettata")
	}
	if _, err := a.PCloudLogin("a@example.com@evil.example.com", "x", true); err == nil {
		t.Error("server diverso da pCloud accettato")
	}
}

// Un accesso riuscito con «ricorda» scrive nel portachiavi e solo lì; senza
// «ricorda» il portachiavi resta intatto. Il server è un finto ssh che dice sì.
func TestPCloudLoginRemembersOnlyWhenAsked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("finto ssh in sh")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakessh")
	// ssh finto che fa da server rsync non serve: basta che rsync riceva un
	// elenco vuoto. Più semplice: sostituire rsync con un finto che esce 0.
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv(sshEnv, fake)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	keyring.MockInit()

	a := NewApp()
	if warn, err := a.PCloudLogin("a@example.com@rsync.pcloud.com", "pw1", false); err != nil || warn != "" {
		t.Fatalf("accesso: %q %v", warn, err)
	}
	if _, err := keyring.Get(keyringService, "a@example.com"); err == nil {
		t.Error("salvata senza che fosse richiesto")
	}
	if a.pcloudPassword("a@example.com") != "pw1" {
		t.Error("in memoria per la sessione sì")
	}
	if warn, err := a.PCloudLogin("a@example.com@rsync.pcloud.com", "pw2", true); err != nil || warn != "" {
		t.Fatalf("accesso: %q %v", warn, err)
	}
	if got, _ := keyring.Get(keyringService, "a@example.com"); got != "pw2" {
		t.Errorf("portachiavi: %q", got)
	}
}
