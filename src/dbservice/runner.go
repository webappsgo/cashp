package dbservice

import (
	"bytes"
	"context"
	"log/slog"
	"time"
)

// loopbackHost is the address an engine is reached at from inside its own
// container. Every management command runs through the orchestrator's exec
// path, so a managed engine never has to accept a connection from outside its
// own network namespace for cashp to administer it.
const loopbackHost = "127.0.0.1"

// contextFor builds the adapter input for one instance and one administrative
// password. The password stays in memory for the length of the operation: it
// is placed only in an exec environment or an owner-only file, never in an
// argv slice and never in a log line.
func (s *Service) contextFor(a adapter, inst *Instance, adminPassword string) engineCtx {
	return engineCtx{
		Instance:      inst,
		AdminUser:     inst.AdminUser,
		AdminPassword: adminPassword,
		Host:          loopbackHost,
		Port:          a.defaultPort(),
	}
}

// engineContext resolves an instance's adapter and decrypts its administrative
// credential so a management command can authenticate.
func (s *Service) engineContext(ctx context.Context, inst *Instance) (adapter, engineCtx, error) {
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, engineCtx{}, err
	}
	cred, err := s.store.GetAdminCredential(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return nil, engineCtx{}, err
	}
	password, err := s.decrypt(cred.Secret)
	if err != nil {
		return nil, engineCtx{}, err
	}
	return a, s.contextFor(a, inst, password), nil
}

// writeFile drops one adapter file inside a container with its required mode
// and ownership.
func (s *Service) writeFile(ctx context.Context, inst *Instance, f fileDrop) error {
	spec := FileSpec{Path: f.Path, Mode: f.Mode, UID: f.UID, GID: f.GID}
	if err := s.orch.WriteFile(ctx, inst.ContainerID, spec, bytes.NewReader(f.Content)); err != nil {
		return ErrInternal(err, "That operation could not be prepared on the instance.")
	}
	return nil
}

// execute runs one command inside an instance: it drops the command's files,
// runs the argv, and always removes the files afterwards whether the command
// succeeded or not. The returned error covers only transport failures; a
// non-zero exit is reported through the result so a probe can interpret it.
func (s *Service) execute(ctx context.Context, inst *Instance, cmd command) (ExecResult, error) {
	if err := checkArgv(cmd.Exec.Argv); err != nil {
		return ExecResult{}, err
	}
	if inst.ContainerID == "" {
		return ExecResult{}, ErrUnavailable("That instance is not running.")
	}
	for _, f := range cmd.Files {
		if err := s.writeFile(ctx, inst, f); err != nil {
			s.cleanup(ctx, inst, cmd.Cleanup)
			return ExecResult{}, err
		}
	}
	res, err := s.orch.Exec(ctx, inst.ContainerID, cmd.Exec)
	s.cleanup(ctx, inst, cmd.Cleanup)
	if err != nil {
		return res, ErrInternal(err, "That operation could not be carried out on the instance.")
	}
	return res, nil
}

// run executes one command and turns a non-zero exit into a typed error. The
// engine's own output is deliberately not copied into the message: it can
// carry a path, a socket name or a fragment of a statement, none of which may
// reach an API response.
func (s *Service) run(ctx context.Context, inst *Instance, cmd command) (ExecResult, error) {
	res, err := s.execute(ctx, inst, cmd)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		s.log.Warn("managed database command failed",
			slog.String("instance_id", inst.ID),
			slog.String("engine", string(inst.Engine)),
			slog.Int("exit_code", res.ExitCode))
		return res, ErrInternal(nil, "The database engine rejected that operation.")
	}
	return res, nil
}

// runAll executes an ordered plan, stopping at the first failure so a partial
// plan never continues past a step that did not apply.
func (s *Service) runAll(ctx context.Context, inst *Instance, cmds []command) ([]ExecResult, error) {
	out := make([]ExecResult, 0, len(cmds))
	for _, cmd := range cmds {
		res, err := s.run(ctx, inst, cmd)
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

// cleanup removes an operation's temporary credential files. Failure to remove
// one is logged and never returned: it must not mask the operation's own
// result, and the files are owner-only inside a container cashp controls.
func (s *Service) cleanup(ctx context.Context, inst *Instance, paths []string) {
	for _, path := range paths {
		if err := s.orch.RemoveFile(ctx, inst.ContainerID, path); err != nil {
			s.log.Warn("managed database could not remove a temporary credential file",
				slog.String("instance_id", inst.ID))
		}
	}
}

// waitReady polls an engine's own protocol-level health probe until it answers
// healthy. The bound is a probe count rather than a wall-clock deadline so the
// loop terminates identically under an injected clock.
func (s *Service) waitReady(ctx context.Context, a adapter, c engineCtx) error {
	for attempt := 0; attempt < s.readyAttempts; attempt++ {
		state, _ := s.probe(ctx, a, c)
		if state == HealthHealthy || state == HealthDegraded {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrUnavailable("That instance did not become ready.")
		case <-time.After(s.readyInterval):
		}
	}
	return ErrUnavailable("That instance did not become ready.")
}

// probe runs the engine's health command and lets the adapter interpret it. A
// transport failure is itself an unhealthy answer, so a probe never returns an
// error: an instance that cannot be reached is unhealthy by definition.
func (s *Service) probe(ctx context.Context, a adapter, c engineCtx) (HealthState, string) {
	res, err := s.execute(ctx, c.Instance, a.healthCommand(c))
	if err != nil {
		return HealthUnhealthy, "The instance did not answer its health check."
	}
	return a.parseHealth(res)
}
