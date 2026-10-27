// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// pCloud speaks rsync over SSH (port 22): the account's e-mail is the user
// name and the account's password the password. Both hosts are documented in
// pCloud's help center ("Connect to pCloud Using WebDAV and rsync").
//
// A pCloud destination is stored like any other remote one, as the plain
// string rsync understands: "mario@example.com@rsync.pcloud.com:/Backup/".
// Nothing about it lives in the profile except that string — in particular no
// password: profiles.json is exported and shared, and a secret there would
// travel with it.
//
// The password goes to the system's own secret store (macOS Keychain, Windows
// Credential Manager, the Secret Service of GNOME/KDE on Linux), once, with the
// user's consent, and is read back from there. From then on it is kept in
// memory and handed to ssh through SSH_ASKPASS, never through the command line
// or a file. Where no store exists (a bare Linux session) the password lives in
// memory for the session only and the user is told so.
const (
	pcloudHostUS = "rsync.pcloud.com"
	pcloudHostEU = "ersync.pcloud.com"

	// loginPrefix starts the error the run methods return when a pCloud
	// account has no password yet in this session; the window turns it into the
	// password dialog and retries. What follows is "user@host".
	loginPrefix = "pcloud-login:"

	// Environment variables. pcloudSecretsEnv is the way a window hands the
	// passwords to the detached supervisor it starts (a process environment is
	// readable only by the same user, and leaves nothing on disk).
	// askpassPassEnv exists only in the environment of rsync's own children: it
	// is what makes this binary behave as the SSH_ASKPASS helper instead of
	// opening a window.
	pcloudSecretsEnv = "RSYNCGUI_PCLOUD_SECRETS"
	askpassPassEnv   = "RSYNCGUI_ASKPASS_PW"
	// keyringService names our entries in the system secret store; the account
	// is the pCloud e-mail.
	keyringService = "RsyncGUI pCloud"
	// sshEnv replaces the "ssh" command, for the tests.
	sshEnv = "RSYNCGUI_SSH"
)

// pcloudAccount splits a remote spec into the account (e-mail) and the host
// when it points at pCloud. The user part may arrive wrapped in quotes, as in
// pCloud's own examples written for a shell.
func pcloudAccount(spec string) (email, host string, ok bool) {
	idx := strings.Index(spec, ":")
	if idx <= 0 || !isRemote(spec) {
		return "", "", false
	}
	head := spec[:idx]
	at := strings.LastIndex(head, "@")
	if at <= 0 {
		return "", "", false
	}
	host = strings.ToLower(head[at+1:])
	if host != pcloudHostUS && host != pcloudHostEU {
		return "", "", false
	}
	email = strings.Trim(head[:at], `"'`)
	if email == "" {
		return "", "", false
	}
	return email, host, true
}

func isPCloud(spec string) bool {
	_, _, ok := pcloudAccount(spec)
	return ok
}

// pcloudSpec builds the stored form of a pCloud folder. region is "eu" or
// "us"; folder is the path inside the account.
func pcloudSpec(region, email, folder string) string {
	host := pcloudHostUS
	if strings.EqualFold(region, "eu") {
		host = pcloudHostEU
	}
	folder = strings.TrimSpace(folder)
	if !strings.HasPrefix(folder, "/") {
		folder = "/" + folder
	}
	return strings.TrimSpace(email) + "@" + host + ":" + folder
}

// pcloudNormalize drops the shell quotes around the user part: rsync is not
// run through a shell and would send them to the server as part of the name.
// The host comes back in lower case (pcloudAccount accepts it in any case).
func pcloudNormalize(spec string) string {
	email, host, ok := pcloudAccount(spec)
	if !ok {
		return spec
	}
	return email + "@" + host + spec[strings.Index(spec, ":"):]
}

// anyPCloud reports whether a copy touches pCloud at either end.
func anyPCloud(sources []string, dest string) bool {
	if isPCloud(dest) {
		return true
	}
	for _, s := range sources {
		if isPCloud(s) {
			return true
		}
	}
	return false
}

// pcloudSSH is the value of rsync's -e. accept-new trusts pCloud's host key on
// first contact and refuses it if it ever changes, so the run never stops on a
// yes/no question nobody can answer; the keep-alive stops a long copy from
// being dropped by an idle NAT.
func pcloudSSH() string {
	bin := os.Getenv(sshEnv)
	if bin == "" {
		bin = "ssh"
	}
	return bin + " -o StrictHostKeyChecking=accept-new" +
		" -o PreferredAuthentications=password,keyboard-interactive" +
		" -o NumberOfPasswordPrompts=1 -o ConnectTimeout=30" +
		" -o ServerAliveInterval=30 -o ServerAliveCountMax=6"
}

// pcloudCopyArgs are the rsync options for a copy that touches pCloud: -a
// minus what pCloud's own example turns off (--no-times, --no-links,
// --no-specials, --no-devices), plus permissions and ownership, which pCloud
// normalises and cannot keep.
//
// Without timestamps rsync's "same size and mtime" test would find every file
// different on every run; --size-only restores incremental copies. The price
// is that a file edited without changing its size is not re-sent: -c
// (checksum) is the safe alternative and wins when the profile asks for it.
func pcloudCopyArgs(opts SyncOptions) []string {
	args := []string{
		"--no-perms", "--no-owner", "--no-group",
		"--no-times", "--no-links", "--no-specials", "--no-devices",
		"-e", pcloudSSH(),
	}
	if !opts.Checksum {
		args = append(args, "--size-only")
	}
	return args
}

// pcloudHint explains the exit codes that mean "could not log in / reach the
// server" in words, since rsync itself only says "connection unexpectedly
// closed".
func pcloudHint(exitCode int, sources []string, dest string) string {
	if exitCode != 255 && exitCode != 12 && exitCode != 5 {
		return ""
	}
	if !anyPCloud(sources, dest) {
		return ""
	}
	return " — accesso a pCloud non riuscito: se hai cambiato la password usa «Dimentica password» in «+ pCloud…» per inserirla di nuovo; con la verifica in due passaggi approva l'e-mail «New login attempt» e riprova"
}

// --- the askpass helper -------------------------------------------------------

// runAskpassIfRequested makes the binary act as ssh's askpass program when it
// was started by ssh from one of our rsync runs. ssh executes
// $SSH_ASKPASS "<prompt>" and reads the answer from its standard output.
func runAskpassIfRequested() bool {
	pw, ok := os.LookupEnv(askpassPassEnv)
	if !ok {
		return false
	}
	prompt := ""
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	if strings.Contains(strings.ToLower(prompt), "yes/no") {
		fmt.Println("yes")
	} else {
		fmt.Println(pw)
	}
	return true
}

// --- passwords ------------------------------------------------------------------

// pcloudPassword returns the password of an account: the session's memory
// first, then the system secret store (kept in memory afterwards, so the store
// is asked once and a locked keyring does not prompt on every copy). "" means
// there is none.
func (a *App) pcloudPassword(email string) string {
	a.mu.Lock()
	pw := a.pcloud[email]
	a.mu.Unlock()
	if pw != "" {
		return pw
	}
	// Outside the lock: a locked keyring may wait for the user.
	pw, err := keyring.Get(keyringService, email)
	if err != nil || pw == "" {
		return ""
	}
	a.mu.Lock()
	if a.pcloud == nil {
		a.pcloud = map[string]string{}
	}
	a.pcloud[email] = pw
	a.mu.Unlock()
	return pw
}

// pcloudSpecs lists the remote specs a set of profiles reads from or writes to.
func pcloudAccountsOf(list []SyncProfile) (emails []string, hosts map[string]string) {
	hosts = map[string]string{}
	for _, p := range list {
		for _, spec := range append(append([]string{}, p.Sources...), p.Destinations...) {
			if email, host, ok := pcloudAccount(spec); ok {
				if _, seen := hosts[email]; !seen {
					emails = append(emails, email)
					hosts[email] = host
				}
			}
		}
	}
	return emails, hosts
}

// pcloudNeedsLogin returns the "user@host" of the first pCloud account used by
// the profiles for which there is no password, or "".
func (a *App) pcloudNeedsLogin(list []SyncProfile) string {
	emails, hosts := pcloudAccountsOf(list)
	for _, email := range emails {
		if a.pcloudPassword(email) == "" {
			return email + "@" + hosts[email]
		}
	}
	return ""
}

// pcloudSecretsFor returns the passwords of the accounts the profiles use.
func (a *App) pcloudSecretsFor(list []SyncProfile) map[string]string {
	emails, _ := pcloudAccountsOf(list)
	out := map[string]string{}
	for _, email := range emails {
		if pw := a.pcloudPassword(email); pw != "" {
			out[email] = pw
		}
	}
	return out
}

// PCloudLogin checks the credentials against the server, keeps the password for
// the session and, if remember is set, puts it in the system secret store.
// userHost is the "user@host" of the error that asked for the login. A wrong
// password is refused here, before any copy starts.
//
// The returned text is a warning, not an error: the login worked but the
// password could not be saved (no secret store on this system).
func (a *App) PCloudLogin(userHost, password string, remember bool) (string, error) {
	at := strings.LastIndex(userHost, "@")
	if at <= 0 || password == "" {
		return "", errors.New("inserisci la password dell'account pCloud")
	}
	email, host := userHost[:at], strings.ToLower(userHost[at+1:])
	if host != pcloudHostUS && host != pcloudHostEU {
		return "", errors.New("server pCloud sconosciuto")
	}
	if err := checkPCloudLogin(email, host, password); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.pcloud == nil {
		a.pcloud = map[string]string{}
	}
	a.pcloud[email] = password
	a.mu.Unlock()
	if !remember {
		return "", nil
	}
	if err := keyring.Set(keyringService, email, password); err != nil {
		return "Accesso riuscito, ma la password non è stata salvata (" + err.Error() +
			"): resta valida finché l'app è aperta e verrà richiesta di nuovo al prossimo avvio.", nil
	}
	return "", nil
}

// PCloudForget removes an account's password from the secret store and from
// memory, so the next copy asks for it again (changed password, shared computer).
func (a *App) PCloudForget(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("inserisci l'e-mail dell'account")
	}
	a.mu.Lock()
	delete(a.pcloud, email)
	a.mu.Unlock()
	if err := keyring.Delete(keyringService, email); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("impossibile togliere la password dal portachiavi: %v", err)
	}
	return nil
}

// PCloudSpec lets the editor build the stored string of a pCloud folder.
func (a *App) PCloudSpec(region, email, folder string) (string, error) {
	if !strings.Contains(strings.TrimSpace(email), "@") {
		return "", errors.New("inserisci l'e-mail dell'account pCloud")
	}
	return pcloudSpec(region, email, folder), nil
}

// checkPCloudLogin lists the account's root: the cheapest call that needs a
// working login, and the one that makes pCloud send its "new login" e-mail.
func checkPCloudLogin(email, host, password string) error {
	rsyncBin, err := exec.LookPath("rsync")
	if err != nil {
		return errors.New("rsync non trovato nel PATH: installalo per usare questa app")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := newRsyncCmd(ctx, rsyncBin, "--list-only", "-e", pcloudSSH(), email+"@"+host+":/")
	env, err := askpassEnv(map[string]string{email: password}, email)
	if err != nil {
		return err
	}
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return errors.New("pCloud non ha risposto in tempo")
	}
	detail := strings.TrimSpace(string(out))
	if i := strings.LastIndex(detail, "\n"); i >= 0 {
		detail = strings.TrimSpace(detail[i+1:])
	}
	return fmt.Errorf("accesso non riuscito (password errata, oppure approva l'e-mail «New login attempt» di pCloud e riprova)%s", suffixDetail(detail))
}

func suffixDetail(d string) string {
	if d == "" {
		return ""
	}
	return ": " + d
}

// --- environment of the children ------------------------------------------------

// askpassEnv is the environment for an rsync that must log in as email: the
// parent's own plus the three variables that make ssh ask this binary for the
// password. SSH_ASKPASS_REQUIRE=force (OpenSSH 8.4+) uses the helper even with
// a terminal around; DISPLAY covers older ssh, which only uses the helper when
// it has a display and no terminal.
func askpassEnv(secrets map[string]string, email string) ([]string, error) {
	pw, ok := secrets[email]
	if !ok || pw == "" {
		return nil, errors.New(loginPrefix + email)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		// A supervisor's carrier variable must not reach rsync and ssh.
		if strings.HasPrefix(kv, pcloudSecretsEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"SSH_ASKPASS="+self,
		"SSH_ASKPASS_REQUIRE=force",
		askpassPassEnv+"="+pw,
	)
	if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=:0")
	}
	return env, nil
}

// pcloudChildEnv returns the environment to give an rsync that copies
// sources → dest, or nil (inherit) when pCloud is not involved. One rsync run
// can log in as one account only.
func pcloudChildEnv(secrets map[string]string, sources []string, dest string) ([]string, error) {
	accounts := map[string]bool{}
	for _, s := range append(append([]string{}, sources...), dest) {
		if email, _, ok := pcloudAccount(s); ok {
			accounts[email] = true
		}
	}
	switch len(accounts) {
	case 0:
		return nil, nil
	case 1:
		for email := range accounts {
			return askpassEnv(secrets, email)
		}
	}
	names := make([]string, 0, len(accounts))
	for e := range accounts {
		names = append(names, e)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("una copia non può usare due account pCloud insieme (%s)", strings.Join(names, ", "))
}

// withSecretsCarrier adds the passwords to a supervisor's environment.
func withSecretsCarrier(cmd *exec.Cmd, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(), pcloudSecretsEnv+"="+string(raw))
	return nil
}

// takeSecretsCarrier reads and removes the passwords a window passed to this
// supervisor.
func takeSecretsCarrier() map[string]string {
	raw := os.Getenv(pcloudSecretsEnv)
	if raw == "" {
		return nil
	}
	_ = os.Unsetenv(pcloudSecretsEnv)
	var out map[string]string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}
