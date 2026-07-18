package main

import (
	"fmt"

	"github.com/gen2brain/beeep"
)

// notifyTitle is what the OS shows as the sender of every notification.
const notifyTitle = "RsyncGUI"

// notify posts a desktop notification, best effort and never blocking.
//
// It is deliberately fire-and-forget: on Linux a missing notification daemon,
// on macOS a user who denied the permission, on Windows a locked-down session
// all make this fail, and none of them is a reason to interrupt or slow down a
// copy that is working. Failures go to the log, where they are visible without
// getting in the way.
//
// beeep talks to the native notifier per platform: WinRT/PowerShell on Windows,
// terminal-notifier or osascript on macOS, D-Bus or notify-send on Linux.
func (a *App) notify(message string) {
	// No window attached (tests, or before startup): nothing to notify from.
	if a.ctx == nil {
		return
	}
	go func() {
		if err := beeep.Notify(notifyTitle, message, ""); err != nil {
			a.emitLog(fmt.Sprintf("(notifica di sistema non riuscita: %v)\n", err))
		}
	}()
}

// notifyForStatus turns the outcome of one profile into a notification.
func (a *App) notifyForStatus(status, profileName, detail string) {
	if msg := statusNotification(status, profileName, detail); msg != "" {
		a.notify(msg)
	}
}

// statusNotification builds the text for one finished profile, or "" when the
// outcome does not deserve a notification.
//
// An aborted run is deliberately silent: the user just pressed Interrompi and
// is looking at the window, so telling them what they did is pure noise.
func statusNotification(status, profileName, detail string) string {
	switch status {
	case "success":
		return fmt.Sprintf("✓ %s — copia completata", profileName)
	case "partial":
		return fmt.Sprintf("⚠ %s — completata, ma alcuni file non sono stati copiati", profileName)
	case "failed":
		if detail != "" {
			return fmt.Sprintf("✗ %s — fallita: %s", profileName, truncate(detail, 120))
		}
		return fmt.Sprintf("✗ %s — fallita", profileName)
	default:
		return ""
	}
}

// truncate keeps a notification body short: the OS clips long text anyway, and
// a cut mid-word reads worse than an explicit ellipsis.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
