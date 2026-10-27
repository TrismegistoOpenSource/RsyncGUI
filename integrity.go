// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync"
	"time"

	"rsyncgui/internal/autoverifica"
)

// appVersion must match productVersion in wails.json (TestVersionMatchesWails).
const appVersion = "3.0.0"

// releaseRepo is where the signed release lives; the startup self-check
// downloads its signature from there (see internal/autoverifica).
const releaseRepo = "TrismegistoOpenSource/RsyncGUI"

// integrityState holds the outcome of the startup self-check.
type integrityState struct {
	once  sync.Once
	state autoverifica.Stato
}

// VerifyIntegrity checks that the program's files are intact and, when the
// release is public, signed by its publisher (internal/autoverifica). The
// frontend calls it after the first paint, so it never delays startup; it runs
// once and later calls return the same outcome. The last outcome is also
// written to the config folder (autoverifica-stato.json).
func (a *App) VerifyIntegrity() autoverifica.Stato {
	a.integrity.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		dir, _ := configDir()
		write := func(s autoverifica.Stato) {
			if dir != "" {
				_ = autoverifica.ScriviStato(dir, s)
			}
		}
		a.integrity.state = autoverifica.Verifica(ctx, autoverifica.Configurazione{
			Versione:     appVersion,
			Repo:         releaseRepo,
			CartellaDati: dir,
			Avanzamento:  write, // the local result is on disk at once, before the network check
		})
		write(a.integrity.state)
	})
	return a.integrity.state
}
