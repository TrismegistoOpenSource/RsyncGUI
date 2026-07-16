package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type SyncOptions struct {
	Checksum bool `json:"checksum"`
	Delete   bool `json:"delete"`
	DryRun   bool `json:"dryRun"`
	Compress bool `json:"compress"`
	Verbose  bool `json:"verbose"`
	Inplace  bool `json:"inplace"`
	// ExcludeSystemFiles defaults to true, including for profiles saved before
	// this option existed — see UnmarshalJSON.
	ExcludeSystemFiles bool     `json:"excludeSystemFiles"`
	CustomExcludes     []string `json:"customExcludes,omitempty"`
}

// UnmarshalJSON turns ExcludeSystemFiles on when the key is absent: a profile
// written by an older version must still get the exclusions by default, and a
// plain bool would silently decode to false.
func (o *SyncOptions) UnmarshalJSON(data []byte) error {
	type alias SyncOptions
	o.ExcludeSystemFiles = true
	return json.Unmarshal(data, (*alias)(o))
}

// systemFileExcludes are files the OS generates on its own: Finder metadata,
// thumbnail caches, trash and indexing folders. They hold no user data, get
// rewritten constantly, and are exactly what tends to fail on network/cloud
// destinations.
var systemFileExcludes = []string{
	// macOS
	".DS_Store",
	"._*", // AppleDouble: resource forks / xattrs on non-native filesystems
	".Spotlight-V100",
	".DocumentRevisions-V100",
	".TemporaryItems",
	".Trashes",
	".fseventsd",
	".VolumeIcon.icns",
	".apdisk",
	".localized",
	// Windows
	"Thumbs.db",
	"ehthumbs.db",
	"desktop.ini",
	"$RECYCLE.BIN",
	// Linux
	".Trash-*",
	".directory",
}

// SyncProfile: one entity = N sources synced to M destinations
// (rsync natively accepts many sources but only one destination per run,
// so execution loops over destinations sequentially).
type SyncProfile struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Sources      []string    `json:"sources"`
	Destinations []string    `json:"destinations"`
	Tag          string      `json:"tag"`
	Options      SyncOptions `json:"options"`
}

// UnmarshalJSON also accepts the 1.x schema (single "source"/"destination"),
// so existing profiles.json files load unchanged.
func (p *SyncProfile) UnmarshalJSON(data []byte) error {
	type alias SyncProfile
	aux := struct {
		*alias
		LegacySource string `json:"source"`
		LegacyDest   string `json:"destination"`
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(p.Sources) == 0 && aux.LegacySource != "" {
		p.Sources = []string{aux.LegacySource}
	}
	if len(p.Destinations) == 0 && aux.LegacyDest != "" {
		p.Destinations = []string{aux.LegacyDest}
	}
	return nil
}

// MarshalJSON keeps writing the legacy keys (first source/destination) so the
// 1.x app can still read the first pair of each profile.
func (p SyncProfile) MarshalJSON() ([]byte, error) {
	type alias SyncProfile
	aux := struct {
		alias
		LegacySource string `json:"source"`
		LegacyDest   string `json:"destination"`
	}{alias: alias(p)}
	if len(p.Sources) > 0 {
		aux.LegacySource = p.Sources[0]
	}
	if len(p.Destinations) > 0 {
		aux.LegacyDest = p.Destinations[0]
	}
	return json.Marshal(aux)
}

type AppState struct {
	Profiles   []SyncProfile `json:"profiles"`
	ConfigPath string        `json:"configPath"`
	RsyncPath  string        `json:"rsyncPath"`
	Busy       bool          `json:"busy"`
}

type App struct {
	ctx      context.Context
	mu       sync.Mutex
	profiles []SyncProfile
	busy     bool
	logOpen  bool
	// abort cancels the running queue and kills the rsync process in flight.
	abort context.CancelFunc
}

// logPanelWidth is how much the window grows to the right when the log panel
// is shown, kept in sync with .log-panel width in style.css.
const logPanelWidth = 400

func NewApp() *App {
	return &App{profiles: []SyncProfile{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.load()
}

// --- persistence -----------------------------------------------------------

// configDir resolves per OS via os.UserConfigDir:
// macOS ~/Library/Application Support, Linux ~/.config, Windows %AppData%.
// RSYNCGUI_CONFIG_DIR overrides it, so tests never write to the real profiles.
func configDir() (string, error) {
	dir := os.Getenv("RSYNCGUI_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "RsyncGUI")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func profilesPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles.json"), nil
}

func (a *App) load() {
	path, err := profilesPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var decoded []SyncProfile
	if json.Unmarshal(data, &decoded) == nil {
		a.mu.Lock()
		a.profiles = decoded
		a.mu.Unlock()
	}
}

// backupPath holds the previous contents of profiles.json: one copy, rewritten
// before every save, so a bad save (or a mistake) is always one `cp` from undo.
func backupPath() (string, error) {
	path, err := profilesPath()
	if err != nil {
		return "", err
	}
	return path + ".bak", nil
}

func (a *App) save() error {
	path, err := profilesPath()
	if err != nil {
		return err
	}

	// Copy the current file aside before it gets replaced. A missing or
	// unreadable file just means there is nothing worth backing up yet, and a
	// failed backup must never block the user from saving their work.
	if prev, readErr := os.ReadFile(path); readErr == nil && len(prev) > 0 {
		if bak, bakErr := backupPath(); bakErr == nil {
			_ = os.WriteFile(bak, prev, 0o644)
		}
	}

	a.mu.Lock()
	data, err := json.MarshalIndent(a.profiles, "", "  ")
	a.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- bound methods (frontend API) -------------------------------------------

func (a *App) GetState() AppState {
	path, _ := profilesPath()
	rsync, _ := exec.LookPath("rsync")
	a.mu.Lock()
	defer a.mu.Unlock()
	return AppState{
		Profiles:   append([]SyncProfile{}, a.profiles...),
		ConfigPath: path,
		RsyncPath:  rsync,
		Busy:       a.busy,
	}
}

func cleanList(list []string) []string {
	var out []string
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) SaveProfile(p SyncProfile) ([]SyncProfile, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Tag = strings.TrimSpace(p.Tag)
	p.Sources = cleanList(p.Sources)
	p.Destinations = cleanList(p.Destinations)
	p.Options.CustomExcludes = cleanList(p.Options.CustomExcludes)
	if len(p.Sources) == 0 || len(p.Destinations) == 0 {
		return nil, errors.New("serve almeno una sorgente e una destinazione")
	}
	if p.Name == "" {
		p.Name = filepath.Base(strings.TrimRight(p.Sources[0], "/\\"))
	}

	a.mu.Lock()
	if p.ID == "" {
		p.ID = strings.ToUpper(uuid.NewString())
		a.profiles = append(a.profiles, p)
	} else {
		found := false
		for i := range a.profiles {
			if a.profiles[i].ID == p.ID {
				a.profiles[i] = p
				found = true
				break
			}
		}
		if !found {
			a.profiles = append(a.profiles, p)
		}
	}
	result := append([]SyncProfile{}, a.profiles...)
	a.mu.Unlock()

	if err := a.save(); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *App) DeleteProfiles(ids []string) ([]SyncProfile, error) {
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	a.mu.Lock()
	kept := a.profiles[:0]
	for _, p := range a.profiles {
		if !idSet[p.ID] {
			kept = append(kept, p)
		}
	}
	a.profiles = append([]SyncProfile{}, kept...)
	result := append([]SyncProfile{}, a.profiles...)
	a.mu.Unlock()

	if err := a.save(); err != nil {
		return nil, err
	}
	return result, nil
}

// ReorderProfiles persists the manual order: profiles are rewritten to match
// the given id sequence. Ids not present are ignored; any stored profile not
// listed is kept at the end (defensive against a stale frontend list).
func (a *App) ReorderProfiles(ids []string) ([]SyncProfile, error) {
	a.mu.Lock()
	byID := map[string]SyncProfile{}
	for _, p := range a.profiles {
		byID[p.ID] = p
	}
	var ordered []SyncProfile
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			ordered = append(ordered, p)
			delete(byID, id)
		}
	}
	for _, p := range a.profiles {
		if _, ok := byID[p.ID]; ok {
			ordered = append(ordered, p)
		}
	}
	a.profiles = ordered
	result := append([]SyncProfile{}, a.profiles...)
	a.mu.Unlock()

	if err := a.save(); err != nil {
		return nil, err
	}
	return result, nil
}

// SetLogVisible grows or shrinks the window by logPanelWidth so the log opens
// as a side extension of the window rather than covering the main UI. The
// logOpen guard keeps repeated show/hide calls from stacking the resize.
func (a *App) SetLogVisible(visible bool) {
	if visible == a.logOpen {
		return
	}
	w, h := runtime.WindowGetSize(a.ctx)
	if visible {
		runtime.WindowSetSize(a.ctx, w+logPanelWidth, h)
	} else {
		runtime.WindowSetSize(a.ctx, w-logPanelWidth, h)
	}
	a.logOpen = visible
}

func (a *App) ChooseDirectory(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                title,
		CanCreateDirectories: true,
	})
}

// ExportProfiles writes the current list to a user-chosen JSON file,
// starting from the app's own config folder so the location is obvious.
func (a *App) ExportProfiles() (string, error) {
	dir, _ := configDir()
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "Esporta configurazioni",
		DefaultDirectory: dir,
		DefaultFilename:  "RsyncGUI-profili.json",
		Filters:          []runtime.FileFilter{{DisplayName: "JSON", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	a.mu.Lock()
	data, err := json.MarshalIndent(a.profiles, "", "  ")
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ImportProfiles merges profiles from a chosen file: same id overwrites,
// new ids are appended — importing an old export never duplicates entries.
func (a *App) ImportProfiles() (int, error) {
	dir, _ := configDir()
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Importa configurazioni",
		DefaultDirectory: dir,
		Filters:          []runtime.FileFilter{{DisplayName: "JSON", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return -1, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	var imported []SyncProfile
	if err := json.Unmarshal(data, &imported); err != nil {
		return -1, fmt.Errorf("file non valido: %w", err)
	}

	a.mu.Lock()
	for _, p := range imported {
		replaced := false
		for i := range a.profiles {
			if a.profiles[i].ID == p.ID {
				a.profiles[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			a.profiles = append(a.profiles, p)
		}
	}
	a.mu.Unlock()

	if err := a.save(); err != nil {
		return -1, err
	}
	return len(imported), nil
}

func (a *App) RunOne(id string) error {
	a.mu.Lock()
	var target *SyncProfile
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			p := a.profiles[i]
			target = &p
			break
		}
	}
	a.mu.Unlock()
	if target == nil {
		return errors.New("destinazione non trovata")
	}
	return a.enqueue([]SyncProfile{*target})
}

func (a *App) RunTag(tag string) error {
	a.mu.Lock()
	var list []SyncProfile
	for _, p := range a.profiles {
		if p.Tag == tag {
			list = append(list, p)
		}
	}
	a.mu.Unlock()
	if len(list) == 0 {
		return errors.New("nessuna destinazione con questo tag")
	}
	return a.enqueue(list)
}

// RunUntagged runs every profile that has no tag, in sequence like a tag group.
func (a *App) RunUntagged() error {
	a.mu.Lock()
	var list []SyncProfile
	for _, p := range a.profiles {
		if p.Tag == "" {
			list = append(list, p)
		}
	}
	a.mu.Unlock()
	if len(list) == 0 {
		return errors.New("nessuna destinazione senza tag")
	}
	return a.enqueue(list)
}

// --- path availability --------------------------------------------------------

// isRemote reports whether a path is an rsync remote spec (host:path or
// rsync://): those can't be checked locally, so they're always attempted.
func isRemote(path string) bool {
	if strings.HasPrefix(path, "rsync://") {
		return true
	}
	idx := strings.Index(path, ":")
	if idx <= 0 {
		return false
	}
	head := path[:idx]
	if strings.ContainsAny(head, "/\\") {
		return false
	}
	// single letter before ":" is a Windows drive (C:\...), not a host
	return len(head) > 1
}

// readable reports whether a directory can actually be read, not merely
// stat'ed. A dead cloud or network mount (pCloud losing its daemon, an SMB
// share dropping) keeps stat'ing fine while every real I/O fails with
// ENXIO/ENOTCONN — so stat alone would wave through exactly the destinations
// this check exists to skip.
func readable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && err != io.EOF {
		return false
	}
	return true
}

func sourceAvailable(path string) bool {
	if isRemote(path) {
		return true
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		return readable(path)
	}
	return true
}

// destAvailable: the destination itself may not exist yet (rsync creates it),
// so the check falls back to its parent — which must be genuinely readable,
// not just present. Catches unmounted volumes, dead mounts and typos.
func destAvailable(path string) bool {
	if isRemote(path) {
		return true
	}
	probe := path
	if _, err := os.Stat(path); err != nil {
		probe = filepath.Dir(strings.TrimRight(path, "/\\"))
	}
	return readable(probe)
}

// --- runner ------------------------------------------------------------------

type statusEvent struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// enqueue runs the given profiles strictly one after another, never
// concurrently. Unavailable paths are skipped without stopping the queue;
// every problem is repeated in a summary at the end of the log.
func (a *App) enqueue(list []SyncProfile) error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return errors.New("un'esecuzione è già in corso")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.busy = true
	a.abort = cancel
	a.mu.Unlock()

	go func() {
		runtime.EventsEmit(a.ctx, "run:busy", true)
		defer func() {
			cancel() // release the context even on a normal finish
			a.mu.Lock()
			a.busy = false
			a.abort = nil
			a.mu.Unlock()
			runtime.EventsEmit(a.ctx, "run:busy", false)
		}()

		var allIssues []string
		for _, p := range list {
			// Stop between profiles too: aborting must not start the next one.
			if runCtx.Err() != nil {
				runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "aborted"})
				continue
			}

			runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "running"})
			a.emitLog(fmt.Sprintf("\n═══ %s ═══\n", p.Name))

			completed, issues := a.runProfile(runCtx, p)

			if runCtx.Err() != nil {
				runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "aborted"})
				continue
			}

			allIssues = append(allIssues, issues...)
			switch {
			case len(issues) == 0:
				runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "success"})
			case completed > 0:
				runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "partial", Message: strings.Join(issues, "; ")})
			default:
				runtime.EventsEmit(a.ctx, "run:status", statusEvent{ID: p.ID, Status: "failed", Message: strings.Join(issues, "; ")})
			}
		}

		switch {
		case runCtx.Err() != nil:
			a.emitLog("\n⛔ Interrotto. Le copie non ancora eseguite sono state annullate.\n")
		case len(allIssues) > 0:
			a.emitLog("\n─── Riepilogo problemi ───\n")
			for _, issue := range allIssues {
				a.emitLog("• " + issue + "\n")
			}
		default:
			a.emitLog("\nTutto completato senza problemi.\n")
		}
	}()
	return nil
}

// Abort stops the queue and kills the rsync currently running.
func (a *App) Abort() error {
	a.mu.Lock()
	cancel := a.abort
	busy := a.busy
	a.mu.Unlock()

	if !busy || cancel == nil {
		return errors.New("nessuna esecuzione in corso")
	}
	a.emitLog("\n⛔ Interruzione richiesta…\n")
	cancel()
	return nil
}

// VerifyFolder walks a folder and reports every file that the filesystem
// lists (readdir/stat) but that can't actually be opened and read — the
// class of bug found on a QNAP SMB share where the server answers "this file
// exists" to a listing but refuses to serve its content. Read-only: nothing
// is copied, deleted or modified, so it's safe to point at anything, and it
// runs in the background like a sync (shares the same busy/log/Abort).
func (a *App) VerifyFolder(root string) error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return errors.New("un'operazione è già in corso")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.busy = true
	a.abort = cancel
	a.mu.Unlock()

	go func() {
		runtime.EventsEmit(a.ctx, "run:busy", true)
		defer func() {
			cancel()
			a.mu.Lock()
			a.busy = false
			a.abort = nil
			a.mu.Unlock()
			runtime.EventsEmit(a.ctx, "run:busy", false)
		}()

		a.emitLog(fmt.Sprintf("\n═══ Verifica: %s ═══\n", root))
		scanned, broken := a.verifyWalk(runCtx, root)

		switch {
		case runCtx.Err() != nil:
			a.emitLog(fmt.Sprintf("\n⛔ Verifica interrotta dopo %d file controllati.\n", scanned))
		case len(broken) == 0:
			a.emitLog(fmt.Sprintf("\n✓ %d file controllati, tutti apribili.\n", scanned))
		default:
			a.emitLog(fmt.Sprintf("\n─── %d di %d file non apribili (elencati sopra) ───\n", len(broken), scanned))
		}
	}()
	return nil
}

// verifyWalk does the actual traversal: open + a short read per file, enough
// to catch "listed but refuses to serve content" without reading whole
// multi-GB files just to prove they're reachable.
func (a *App) verifyWalk(runCtx context.Context, root string) (scanned int, broken []string) {
	lastReport := time.Now()
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if runCtx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			msg := fmt.Sprintf("%s (%v)", path, err)
			broken = append(broken, msg)
			a.emitLog("✗ " + msg + "\n")
			return nil
		}
		if d.IsDir() {
			return nil
		}

		scanned++
		if time.Since(lastReport) > 2*time.Second {
			a.emitLog(fmt.Sprintf("  …%d file controllati\n", scanned))
			lastReport = time.Now()
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			msg := fmt.Sprintf("%s (%v)", path, openErr)
			broken = append(broken, msg)
			a.emitLog("✗ " + msg + "\n")
			return nil
		}
		defer f.Close()
		buf := make([]byte, 64)
		if _, readErr := f.Read(buf); readErr != nil && readErr != io.EOF {
			msg := fmt.Sprintf("%s (%v)", path, readErr)
			broken = append(broken, msg)
			a.emitLog("✗ " + msg + "\n")
		}
		return nil
	})
	return scanned, broken
}

func (a *App) emitLog(text string) {
	// No window attached yet (or under test): nothing to emit to.
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "run:log", text)
}

// runProfile syncs all available sources into each available destination,
// one rsync run per destination. Unavailable paths are skipped and reported;
// the rest of the profile (and the queue) keeps going.
func (a *App) runProfile(runCtx context.Context, p SyncProfile) (completed int, issues []string) {
	var srcs []string
	for _, s := range p.Sources {
		if sourceAvailable(s) {
			srcs = append(srcs, s)
		} else {
			msg := fmt.Sprintf("%s: sorgente non disponibile, saltata: %s", p.Name, s)
			issues = append(issues, msg)
			a.emitLog("⚠ " + msg + "\n")
		}
	}
	if len(srcs) == 0 {
		msg := fmt.Sprintf("%s: nessuna sorgente disponibile, nulla da copiare", p.Name)
		issues = append(issues, msg)
		a.emitLog("⚠ " + msg + "\n")
		return 0, issues
	}

	for _, dest := range p.Destinations {
		// A profile with many destinations must stop at the current one.
		if runCtx.Err() != nil {
			return completed, issues
		}
		if !destAvailable(dest) {
			msg := fmt.Sprintf("%s: destinazione non disponibile, saltata: %s", p.Name, dest)
			issues = append(issues, msg)
			a.emitLog("⚠ " + msg + "\n")
			continue
		}
		if len(p.Destinations) > 1 {
			a.emitLog(fmt.Sprintf("→ verso %s\n", dest))
		}

		exitCode, err := a.execRsync(runCtx, p.Options, srcs, dest)
		if runCtx.Err() != nil {
			// killed by Abort: whatever came back is moot, not a real failure
			return completed, issues
		}

		switch {
		case err != nil:
			msg := fmt.Sprintf("%s → %s: %s", p.Name, dest, err.Error())
			issues = append(issues, msg)
			a.emitLog("✗ " + msg + "\n")
		case exitCode == 0:
			completed++
			a.emitLog("Completato.\n")
		case isPartialTransferExitCode(exitCode):
			// The bulk of the data did arrive; rsync itself skipped the rest
			// and kept going (typical of a network/cloud mount driver that
			// lists a file but then refuses to open it). Counts as completed,
			// not failed — the log above has the exact file(s) it skipped.
			completed++
			msg := fmt.Sprintf("%s → %s: completata con uno o più file non trasferibili (dettagli nel log sopra)", p.Name, dest)
			issues = append(issues, msg)
			a.emitLog("⚠ " + msg + "\n")
		default:
			msg := fmt.Sprintf("%s → %s: rsync terminato con errore (exit %d)", p.Name, dest, exitCode)
			issues = append(issues, msg)
			a.emitLog("✗ " + msg + "\n")
		}
	}
	return completed, issues
}

// interruptOnCancel makes a cancelled context stop the command the same way
// Ctrl+C does, with SIGINT instead of the default SIGKILL: rsync traps SIGINT
// and removes the partial temp file it was writing, while SIGKILL would leave
// it behind in the destination. WaitDelay is the escape hatch if it ignores the
// signal and hangs.
func interruptOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
}

// rsyncArgs builds the command line for one destination.
//
// -a (archive) is always on, so permissions, ownership, timestamps and symlinks
// are preserved wherever the destination filesystem can actually hold them
// (SMB shares, cloud mounts and volumes mounted `noowners` cannot: they
// synthesise their own — rsync still asks, they just don't keep it).
func rsyncArgs(opts SyncOptions, sources []string, dest string) []string {
	args := []string{"-a"}
	if opts.Checksum {
		args = append(args, "-c")
	}
	if opts.Delete {
		args = append(args, "--delete")
	}
	if opts.DryRun {
		args = append(args, "-n")
	}
	if opts.Compress {
		args = append(args, "-z")
	}
	if opts.Verbose {
		args = append(args, "-v")
	}
	// Writes straight into the destination file instead of the temp-file +
	// rename dance, which is the step that fails on cloud/network mounts.
	if opts.Inplace {
		args = append(args, "--inplace")
	}
	if opts.ExcludeSystemFiles {
		for _, pattern := range systemFileExcludes {
			args = append(args, "--exclude="+pattern)
		}
	}
	for _, pattern := range opts.CustomExcludes {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			args = append(args, "--exclude="+pattern)
		}
	}

	// A directory source syncs its *contents* (trailing slash), matching 1.x.
	for _, src := range sources {
		if fi, statErr := os.Stat(src); statErr == nil && fi.IsDir() && !strings.HasSuffix(src, "/") {
			src += "/"
		}
		args = append(args, src)
	}
	return append(args, dest)
}

// isPartialTransferExitCode reports rsync exit codes that mean the run
// completed but skipped one or more individual files, as opposed to the
// whole run failing. 23: partial transfer due to error — the case a flaky
// network/cloud mount driver produces when it can list a file but then
// refuses to open it (observed with QNAP SMB shares and dead cloud mounts).
// 24: partial transfer due to vanished source files. Both are documented
// rsync exit codes, stable across GNU rsync and openrsync alike, which is
// why the classification lives here instead of parsing implementation-
// specific error text.
func isPartialTransferExitCode(code int) bool {
	return code == 23 || code == 24
}

// execRsync runs one rsync invocation and reports how it ended: exitCode is
// rsync's own exit status (0 = full success; see isPartialTransferExitCode
// for the "some files skipped" case), and err is set only when the process
// itself never produced one (missing binary, failed to start).
func (a *App) execRsync(runCtx context.Context, opts SyncOptions, sources []string, dest string) (exitCode int, err error) {
	rsyncBin, err := exec.LookPath("rsync")
	if err != nil {
		return -1, errors.New("rsync non trovato nel PATH: installalo per usare questa app")
	}

	cmd := exec.CommandContext(runCtx, rsyncBin, rsyncArgs(opts, sources, dest)...)
	interruptOnCancel(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			a.emitLog(scanner.Text() + "\n")
		}
	}()

	if err := cmd.Start(); err != nil {
		pw.Close()
		wg.Wait()
		return -1, err
	}
	runErr := cmd.Wait()
	pw.Close()
	wg.Wait()

	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	// Killed by something other than a normal exit (e.g. a signal our own
	// Abort didn't send) — no usable exit code, treat as a hard failure.
	return -1, fmt.Errorf("rsync terminato con errore: %v", runErr)
}

// Tags is kept for API completeness (the frontend derives tags client-side).
func (a *App) Tags() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	var tags []string
	for _, p := range a.profiles {
		if p.Tag != "" && !seen[p.Tag] {
			seen[p.Tag] = true
			tags = append(tags, p.Tag)
		}
	}
	sort.Strings(tags)
	return tags
}
