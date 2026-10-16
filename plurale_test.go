// SPDX-License-Identifier: MIT

package main

import "testing"

// "Completata in 3s (1 destinazioni)." nel riepilogo di una copia
// (segnalato dalle prove su Linux il 08/10/2026).
func TestPlurale(t *testing.T) {
	casi := map[int]string{0: "0 destinazioni", 1: "1 destinazione", 2: "2 destinazioni"}
	for n, atteso := range casi {
		if got := contaDestinazioni(n); got != atteso {
			t.Errorf("contaDestinazioni(%d) = %q, atteso %q", n, got, atteso)
		}
	}
	if got := plurale(1, "avviso", "avvisi"); got != "1 avviso" {
		t.Errorf("plurale(1) = %q", got)
	}
}
