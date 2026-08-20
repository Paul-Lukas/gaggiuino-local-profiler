package machines

import (
	"testing"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/sse"
)

// TestGaggiuinoLiveClient_IdleSessionTerminates is the #901 code-review
// regression test for the unbounded-reconnect goroutine leak: session()
// used to open a persistent WS session + reconnect goroutine with no upper
// bound, so a dead/unreachable host retried forever once anything had
// called GetLiveSensorSnapshot/GetLiveSystemState for it even once. Uses a
// short idleTimeout injected directly (same-package white-box access, not
// the production 5-minute value) and waits on the session's done channel
// instead of sleep-polling.
func TestGaggiuinoLiveClient_IdleSessionTerminates(t *testing.T) {
	c := newGaggiuinoLiveClient(sse.NewHub())
	c.idleTimeout = 30 * time.Millisecond

	// Port 1 is a well-known "connection refused" target — connectOnce's
	// Dial fails immediately, so run() spends its time in the
	// liveReconnectDelay wait, exactly where ctx cancellation (fired by the
	// idle timer) must interrupt it promptly.
	const baseURL = "http://127.0.0.1:1"

	c.GetLiveSensorSnapshot(baseURL) // opens the session as a side effect

	c.mu.Lock()
	s, ok := c.sessions[baseURL]
	c.mu.Unlock()
	if !ok {
		t.Fatal("expected session() to have created a session")
	}

	select {
	case <-s.done:
		// run() exited — the idle timer fired and cancelled its context.
	case <-time.After(2 * time.Second):
		t.Fatal("session did not terminate within 2s of its idle timeout — reconnect goroutine leaked")
	}

	c.mu.Lock()
	_, stillPresent := c.sessions[baseURL]
	c.mu.Unlock()
	if stillPresent {
		t.Fatal("expired session was not removed from the sessions map")
	}

	// A later call must lazily reopen a brand-new session, same as the
	// very first call did — the bound must not "poison" the host forever.
	c.GetLiveSensorSnapshot(baseURL)
	c.mu.Lock()
	s2, ok := c.sessions[baseURL]
	c.mu.Unlock()
	if !ok {
		t.Fatal("expected a later call to lazily reopen a session")
	}
	if s2 == s {
		t.Fatal("expected a fresh session object after idle eviction, got the same (already-cancelled) one")
	}
}
