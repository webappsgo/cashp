package reporter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/agent/transport"
)

// State is the enrollment record the agent keeps between restarts so a
// registered node does not re-enroll on every service start.
type State struct {
	AgentID        string `json:"agent_id"`
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	Server         string `json:"server"`
	RegisteredAt   string `json:"registered_at"`
	LastHeartbeat  string `json:"last_heartbeat,omitempty"`
	TasksCompleted int    `json:"tasks_completed"`
}

// Registered reports whether this state carries a usable enrollment.
func (s *State) Registered() bool {
	return s != nil && strings.TrimSpace(s.AgentID) != ""
}

// LoadState reads the enrollment record. A missing file yields an empty
// state, which is how a fresh install is detected.
func LoadState(path string) (*State, error) {
	if err := paths.CheckFilePerms(path); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return state, nil
}

// SaveState writes the enrollment record with owner-only permissions.
func SaveState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent state: %w", err)
	}
	return paths.WriteSecureFile(path, append(data, '\n'))
}

// applyRegistration records a fresh enrollment against the active node.
func (s *State) applyRegistration(reg *transport.Registration, server string) {
	s.AgentID = reg.AgentID
	s.Name = reg.Name
	s.Scope = reg.Scope
	s.Server = server
	s.RegisteredAt = time.Now().UTC().Format(time.RFC3339)
}
