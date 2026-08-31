package overlay

import (
	"fmt"
	"math/rand/v2"
	"net"
)

// Backend ports use the same random-unused range the server uses for its own
// port (AI.md PART 5 "Port Rules"). They are never persisted: a fresh port is
// picked each run and the generated torrc / tunnels.conf is rewritten to
// match.
const (
	backendPortMin      = 64000
	backendPortMax      = 64999
	backendPortAttempts = 200
)

// listenLoopback binds a dedicated loopback listener on a random unused port
// in the backend range and returns it with the port it claimed. The listener
// is loopback-only: it exists solely to receive connections forwarded by the
// local Tor process or I2P provider, never to be reachable off-host.
func listenLoopback() (net.Listener, int, error) {
	span := backendPortMax - backendPortMin + 1
	for attempt := 0; attempt < backendPortAttempts; attempt++ {
		port := backendPortMin + rand.IntN(span)
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		return listener, port, nil
	}
	return nil, 0, fmt.Errorf("no free loopback port in %d-%d after %d attempts", backendPortMin, backendPortMax, backendPortAttempts)
}
