package hosting

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// PaaS defaults. A caller may override every one of them per app.
const (
	// defaultKeepReleases is how many superseded releases survive cleanup.
	defaultKeepReleases = 5
	// defaultAppPort is the container port a release listens on.
	defaultAppPort = 8080
	// defaultReplicas is the instance count of a new app.
	defaultReplicas = 1
	// defaultAppMemoryMB bounds a release that names no memory limit.
	defaultAppMemoryMB = 512
	// maxReplicas bounds a scale request.
	maxReplicas = 64
	// maxLogLines bounds how much output a caller may pull at once.
	maxLogLines = 1000
	// maxReleaseLog bounds how much build output is retained per release.
	maxReleaseLog = 16384
	// defaultGitBranch is cloned when neither the app nor the request names one.
	defaultGitBranch = "main"
)

// appReleaseDir is the directory holding one subdirectory per release.
const appReleaseDir = "releases"

// secretMask replaces a secret env value on every read path.
const secretMask = "********"

// CreateAppRequest describes a new PaaS application.
type CreateAppRequest struct {
	// Name is the tenant-unique application name.
	Name string
	// Runtime selects the buildpack the orchestrator applies to source deploys.
	Runtime string
	// GitRemote and GitBranch are the source of a build-from-source deploy.
	GitRemote string
	GitBranch string
	// Domain is an optional verified domain the app is published on.
	Domain string
	// Port is the container port the app listens on.
	Port int
	// Replicas is the desired instance count.
	Replicas int
	// MemoryMB and CPUShares bound one instance.
	MemoryMB  int64
	CPUShares int64
}

// UpdateAppRequest carries the fields a caller wants changed; a nil field is
// left untouched.
type UpdateAppRequest struct {
	Runtime   *string
	GitRemote *string
	GitBranch *string
	Domain    *string
	Port      *int
	MemoryMB  *int64
	CPUShares *int64
}

// DeployRequest describes one deploy attempt. An image deploy runs a prebuilt
// container; otherwise the app's git remote is cloned and built by runtime.
type DeployRequest struct {
	// Image is a container reference to run as-is.
	Image string
	// Command overrides the runtime's default start command.
	Command []string
	// Branch overrides the app's configured branch for this deploy only.
	Branch string
}

// EnvVarView is one environment entry as the panel and API see it. A secret
// value is always masked; the plaintext exists only inside a deploy.
type EnvVarView struct {
	Key       string
	Value     string
	Secret    bool
	UpdatedAt time.Time
}

// CreateApp registers a PaaS application and its release tree.
func (s *Service) CreateApp(ctx context.Context, tenantID string, req CreateAppRequest) (App, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return App{}, err
	}
	if err := ValidateName("name", req.Name); err != nil {
		return App{}, err
	}
	if err := ValidateRuntime(req.Runtime); err != nil {
		return App{}, err
	}
	if err := ValidateGitRemote(req.GitRemote); err != nil {
		return App{}, err
	}
	if err := ValidateGitBranch(req.GitBranch); err != nil {
		return App{}, err
	}

	domain := ""
	if req.Domain != "" {
		normalized, err := ValidateDomain(req.Domain)
		if err != nil {
			return App{}, err
		}
		if err = s.requireOwnedDomain(ctx, tenantID, normalized); err != nil {
			return App{}, err
		}
		domain = normalized
	}
	port := req.Port
	if port == 0 {
		port = defaultAppPort
	}
	if err := validatePort("port", port); err != nil {
		return App{}, err
	}
	replicas := req.Replicas
	if replicas == 0 {
		replicas = defaultReplicas
	}
	if err := validateReplicas(replicas); err != nil {
		return App{}, err
	}
	memory := req.MemoryMB
	if memory == 0 {
		memory = defaultAppMemoryMB
	}
	if err := ValidateQuotaMB("memory_mb", memory); err != nil {
		return App{}, err
	}
	if req.CPUShares < 0 {
		return App{}, invalid("cpu_shares", "must not be negative")
	}

	existing, err := s.store.ListApps(ctx, tenantID)
	if err != nil {
		return App{}, err
	}
	for _, a := range existing {
		if strings.EqualFold(a.Name, req.Name) {
			return App{}, apperr.New(apperr.CodeConflict, 409, "an app with that name already exists")
		}
	}
	if err = s.checkQuota(ctx, tenantID, ResourceApps, int64(len(existing))); err != nil {
		return App{}, err
	}

	now := s.now().UTC()
	app := App{
		ID:        s.newID(),
		TenantID:  tenantID,
		Name:      req.Name,
		Runtime:   req.Runtime,
		GitRemote: req.GitRemote,
		GitBranch: req.GitBranch,
		Domain:    domain,
		Port:      port,
		Replicas:  replicas,
		MemoryMB:  memory,
		CPUShares: req.CPUShares,
		State:     AppCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	releases, err := s.appReleasesDir(app)
	if err != nil {
		return App{}, err
	}
	if err = ensureDir(releases, dirMode); err != nil {
		return App{}, err
	}
	if err = s.store.CreateApp(ctx, app); err != nil {
		return App{}, err
	}
	s.audit(ctx, "hosting.app.create", tenantID, app.ID, "name", app.Name)
	return app, nil
}

// GetApp returns one app owned by the tenant.
func (s *Service) GetApp(ctx context.Context, tenantID, appID string) (App, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return App{}, err
	}
	if err := ValidateID("app", appID); err != nil {
		return App{}, err
	}
	return s.store.GetApp(ctx, tenantID, appID)
}

// ListApps returns every app owned by the tenant.
func (s *Service) ListApps(ctx context.Context, tenantID string) ([]App, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return nil, err
	}
	return s.store.ListApps(ctx, tenantID)
}

// UpdateApp changes app settings. The change takes effect on the next deploy.
func (s *Service) UpdateApp(ctx context.Context, tenantID, appID string, req UpdateAppRequest) (App, error) {
	app, err := s.GetApp(ctx, tenantID, appID)
	if err != nil {
		return App{}, err
	}
	if req.Runtime != nil {
		if err = ValidateRuntime(*req.Runtime); err != nil {
			return App{}, err
		}
		app.Runtime = *req.Runtime
	}
	if req.GitRemote != nil {
		if err = ValidateGitRemote(*req.GitRemote); err != nil {
			return App{}, err
		}
		app.GitRemote = *req.GitRemote
	}
	if req.GitBranch != nil {
		if err = ValidateGitBranch(*req.GitBranch); err != nil {
			return App{}, err
		}
		app.GitBranch = *req.GitBranch
	}
	if req.Domain != nil {
		if *req.Domain == "" {
			app.Domain = ""
		} else {
			normalized, domErr := ValidateDomain(*req.Domain)
			if domErr != nil {
				return App{}, domErr
			}
			if err = s.requireOwnedDomain(ctx, tenantID, normalized); err != nil {
				return App{}, err
			}
			app.Domain = normalized
		}
	}
	if req.Port != nil {
		if err = validatePort("port", *req.Port); err != nil {
			return App{}, err
		}
		app.Port = *req.Port
	}
	if req.MemoryMB != nil {
		if err = ValidateQuotaMB("memory_mb", *req.MemoryMB); err != nil {
			return App{}, err
		}
		app.MemoryMB = *req.MemoryMB
	}
	if req.CPUShares != nil {
		if *req.CPUShares < 0 {
			return App{}, invalid("cpu_shares", "must not be negative")
		}
		app.CPUShares = *req.CPUShares
	}
	app.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return App{}, err
	}
	s.audit(ctx, "hosting.app.update", tenantID, app.ID)
	return app, nil
}

// DeleteApp removes an app, its workload, its releases, and its release tree.
func (s *Service) DeleteApp(ctx context.Context, tenantID, appID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	app, err := s.GetApp(ctx, tenantID, appID)
	if err != nil {
		return err
	}
	if app.WorkloadID != "" && s.orchestrator != nil {
		if err = s.orchestrator.Remove(ctx, app.WorkloadID); err != nil {
			return apperr.Wrap(err, apperr.CodeUnavailable, 503, "the workload could not be removed")
		}
	}
	releases, err := s.store.ListReleases(ctx, tenantID, appID)
	if err != nil {
		return err
	}
	for _, r := range releases {
		if err = s.store.DeleteRelease(ctx, tenantID, r.ID); err != nil {
			return err
		}
	}
	env, err := s.store.ListEnv(ctx, tenantID, appID)
	if err != nil {
		return err
	}
	for _, e := range env {
		if err = s.store.DeleteEnv(ctx, tenantID, appID, e.Key); err != nil {
			return err
		}
	}
	if err = s.store.DeleteApp(ctx, tenantID, appID); err != nil {
		return err
	}
	dir, err := s.appDir(app)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(dir); err != nil {
		return internalErr(err, "the application files could not be removed")
	}
	s.audit(ctx, "hosting.app.delete", tenantID, appID, "name", app.Name)
	return nil
}

// SetEnv stores one environment entry. A secret value is encrypted at rest and
// is never returned in plaintext by any read path.
func (s *Service) SetEnv(ctx context.Context, tenantID, appID, key, value string, secret bool) error {
	if _, err := s.GetApp(ctx, tenantID, appID); err != nil {
		return err
	}
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	if err := ValidateEnvValue(value); err != nil {
		return err
	}
	entry := EnvVar{
		TenantID:  tenantID,
		AppID:     appID,
		Key:       key,
		Secret:    secret,
		UpdatedAt: s.now().UTC(),
	}
	if secret {
		sealed, err := security.Encrypt(s.key, []byte(value))
		if err != nil {
			return internalErr(err, "the value could not be protected")
		}
		entry.Encrypted = sealed
	} else {
		entry.Value = value
	}
	if err := s.store.PutEnv(ctx, entry); err != nil {
		return err
	}
	s.audit(ctx, "hosting.app.env.set", tenantID, appID, "key", key, "secret", secret)
	return nil
}

// ListEnv returns the environment of an app with every secret masked.
func (s *Service) ListEnv(ctx context.Context, tenantID, appID string) ([]EnvVarView, error) {
	if _, err := s.GetApp(ctx, tenantID, appID); err != nil {
		return nil, err
	}
	entries, err := s.store.ListEnv(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	views := make([]EnvVarView, 0, len(entries))
	for _, e := range entries {
		view := EnvVarView{Key: e.Key, Value: e.Value, Secret: e.Secret, UpdatedAt: e.UpdatedAt}
		if e.Secret {
			view.Value = secretMask
		}
		views = append(views, view)
	}
	return views, nil
}

// DeleteEnv removes one environment entry.
func (s *Service) DeleteEnv(ctx context.Context, tenantID, appID, key string) error {
	if _, err := s.GetApp(ctx, tenantID, appID); err != nil {
		return err
	}
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	if err := s.store.DeleteEnv(ctx, tenantID, appID, key); err != nil {
		return err
	}
	s.audit(ctx, "hosting.app.env.delete", tenantID, appID, "key", key)
	return nil
}

// Deploy builds and runs a new release. A prebuilt image runs as-is; otherwise
// the app's git remote is cloned into a fresh release directory and handed to
// the orchestrator for the runtime build.
func (s *Service) Deploy(ctx context.Context, tenantID, appID string, req DeployRequest) (Release, error) {
	app, err := s.GetApp(ctx, tenantID, appID)
	if err != nil {
		return Release{}, err
	}
	if s.orchestrator == nil {
		return Release{}, apperr.New(apperr.CodeUnavailable, 503, "the orchestrator is not configured")
	}
	if app.State == AppDeploying {
		return Release{}, apperr.New(apperr.CodeConflict, 409, "a deploy is already in progress for this app")
	}
	if err = ValidateImageRef(req.Image); err != nil {
		return Release{}, err
	}
	command, err := normalizeCommand(req.Command)
	if err != nil {
		return Release{}, err
	}
	branch := req.Branch
	if branch == "" {
		branch = app.GitBranch
	}
	if branch == "" {
		branch = defaultGitBranch
	}
	if err = ValidateGitBranch(branch); err != nil {
		return Release{}, err
	}
	if req.Image == "" && app.GitRemote == "" {
		return Release{}, invalid("image", "is required when the app has no git remote")
	}

	releases, err := s.store.ListReleases(ctx, tenantID, appID)
	if err != nil {
		return Release{}, err
	}
	var number int64
	for _, r := range releases {
		if r.Number > number {
			number = r.Number
		}
	}
	number++

	now := s.now().UTC()
	release := Release{
		ID:        s.newID(),
		TenantID:  tenantID,
		AppID:     appID,
		Number:    number,
		Image:     req.Image,
		Command:   strings.Join(command, " "),
		Source:    releaseRelPath(number),
		State:     ReleasePending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err = s.store.CreateRelease(ctx, release); err != nil {
		return Release{}, err
	}
	app.State = AppDeploying
	app.UpdatedAt = now
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return Release{}, err
	}

	sourceDir := ""
	if req.Image == "" {
		sourceDir, err = s.releaseDir(app, number)
		if err != nil {
			return Release{}, err
		}
		release.State = ReleaseBuilding
		release.UpdatedAt = s.now().UTC()
		if err = s.store.UpdateRelease(ctx, release); err != nil {
			return Release{}, err
		}
		if err = os.RemoveAll(sourceDir); err != nil {
			return Release{}, internalErr(err, "the release directory could not be prepared")
		}
		out, cloneErr := s.run(ctx, s.cmds.GitClone, branch, app.GitRemote, sourceDir)
		release.Log = s.sanitizeLog(out)
		if cloneErr != nil {
			return s.failRelease(ctx, app, release, cloneErr, "the source could not be fetched")
		}
	}

	env, err := s.deployEnv(ctx, tenantID, appID)
	if err != nil {
		return Release{}, err
	}
	workloadID, err := s.orchestrator.Deploy(ctx, WorkloadSpec{
		TenantID:  tenantID,
		AppID:     appID,
		ReleaseID: release.ID,
		Name:      app.Name,
		Runtime:   app.Runtime,
		Image:     req.Image,
		SourceDir: sourceDir,
		Command:   command,
		Env:       env,
		Replicas:  app.Replicas,
		Port:      app.Port,
		MemoryMB:  app.MemoryMB,
		CPUShares: app.CPUShares,
	})
	if err != nil {
		return s.failRelease(ctx, app, release, err, "the release could not be started")
	}

	if err = s.supersede(ctx, tenantID, releases, ReleaseSuperseded); err != nil {
		return Release{}, err
	}
	previousWorkload := app.WorkloadID

	release.WorkloadID = workloadID
	release.State = ReleaseDeployed
	release.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateRelease(ctx, release); err != nil {
		return Release{}, err
	}
	app.WorkloadID = workloadID
	app.ReleaseID = release.ID
	app.State = AppRunning
	app.UpdatedAt = release.UpdatedAt
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return Release{}, err
	}
	if previousWorkload != "" && previousWorkload != workloadID {
		if err = s.orchestrator.Remove(ctx, previousWorkload); err != nil {
			return Release{}, apperr.Wrap(err, apperr.CodeUnavailable, 503,
				"the previous release could not be retired")
		}
	}
	s.audit(ctx, "hosting.app.deploy", tenantID, appID, "release", release.Number)
	return release, nil
}

// ListReleases returns the releases of an app, newest first.
func (s *Service) ListReleases(ctx context.Context, tenantID, appID string) ([]Release, error) {
	if _, err := s.GetApp(ctx, tenantID, appID); err != nil {
		return nil, err
	}
	releases, err := s.store.ListReleases(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Number > releases[j].Number })
	return releases, nil
}

// Rollback returns an app to an earlier release by starting that release's
// artefacts again. The release that was live is marked rolled back.
func (s *Service) Rollback(ctx context.Context, tenantID, appID, releaseID string) (Release, error) {
	app, err := s.GetApp(ctx, tenantID, appID)
	if err != nil {
		return Release{}, err
	}
	if s.orchestrator == nil {
		return Release{}, apperr.New(apperr.CodeUnavailable, 503, "the orchestrator is not configured")
	}
	if err = ValidateID("release", releaseID); err != nil {
		return Release{}, err
	}
	target, err := s.store.GetRelease(ctx, tenantID, releaseID)
	if err != nil {
		return Release{}, err
	}
	if target.AppID != appID {
		return Release{}, notFound("release")
	}
	if target.ID == app.ReleaseID {
		return Release{}, apperr.New(apperr.CodeConflict, 409, "that release is already live")
	}
	switch target.State {
	case ReleaseDeployed, ReleaseSuperseded, ReleaseRolledBack:
	default:
		return Release{}, apperr.New(apperr.CodeConflict, 409, "only a release that deployed successfully can be restored")
	}

	sourceDir := ""
	if target.Image == "" {
		sourceDir, err = s.releaseDir(app, target.Number)
		if err != nil {
			return Release{}, err
		}
		if _, statErr := os.Stat(sourceDir); statErr != nil {
			return Release{}, apperr.New(apperr.CodeConflict, 409, "the files of that release are no longer available")
		}
	}
	env, err := s.deployEnv(ctx, tenantID, appID)
	if err != nil {
		return Release{}, err
	}
	workloadID, err := s.orchestrator.Deploy(ctx, WorkloadSpec{
		TenantID:  tenantID,
		AppID:     appID,
		ReleaseID: target.ID,
		Name:      app.Name,
		Runtime:   app.Runtime,
		Image:     target.Image,
		SourceDir: sourceDir,
		Command:   splitCommand(target.Command),
		Env:       env,
		Replicas:  app.Replicas,
		Port:      app.Port,
		MemoryMB:  app.MemoryMB,
		CPUShares: app.CPUShares,
	})
	if err != nil {
		return Release{}, apperr.Wrap(err, apperr.CodeUnavailable, 503, "the release could not be started")
	}

	now := s.now().UTC()
	if app.ReleaseID != "" {
		if current, curErr := s.store.GetRelease(ctx, tenantID, app.ReleaseID); curErr == nil {
			current.State = ReleaseRolledBack
			current.UpdatedAt = now
			if err = s.store.UpdateRelease(ctx, current); err != nil {
				return Release{}, err
			}
		} else if !apperr.Is(curErr, apperr.CodeNotFound) {
			return Release{}, curErr
		}
	}
	previousWorkload := app.WorkloadID
	target.State = ReleaseDeployed
	target.WorkloadID = workloadID
	target.UpdatedAt = now
	if err = s.store.UpdateRelease(ctx, target); err != nil {
		return Release{}, err
	}
	app.ReleaseID = target.ID
	app.WorkloadID = workloadID
	app.State = AppRunning
	app.UpdatedAt = now
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return Release{}, err
	}
	if previousWorkload != "" && previousWorkload != workloadID {
		if err = s.orchestrator.Remove(ctx, previousWorkload); err != nil {
			return Release{}, apperr.Wrap(err, apperr.CodeUnavailable, 503,
				"the previous release could not be retired")
		}
	}
	s.audit(ctx, "hosting.app.rollback", tenantID, appID, "release", target.Number)
	return target, nil
}

// StartApp starts the current workload of an app.
func (s *Service) StartApp(ctx context.Context, tenantID, appID string) (App, error) {
	app, workloadID, err := s.runningApp(ctx, tenantID, appID)
	if err != nil {
		return App{}, err
	}
	if err = s.orchestrator.Start(ctx, workloadID); err != nil {
		return App{}, apperr.Wrap(err, apperr.CodeUnavailable, 503, "the app could not be started")
	}
	app.State = AppRunning
	app.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return App{}, err
	}
	s.audit(ctx, "hosting.app.start", tenantID, appID)
	return app, nil
}

// StopApp stops the current workload of an app.
func (s *Service) StopApp(ctx context.Context, tenantID, appID string) (App, error) {
	app, workloadID, err := s.runningApp(ctx, tenantID, appID)
	if err != nil {
		return App{}, err
	}
	if err = s.orchestrator.Stop(ctx, workloadID); err != nil {
		return App{}, apperr.Wrap(err, apperr.CodeUnavailable, 503, "the app could not be stopped")
	}
	app.State = AppStopped
	app.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return App{}, err
	}
	s.audit(ctx, "hosting.app.stop", tenantID, appID)
	return app, nil
}

// ScaleApp changes the instance count of the current workload.
func (s *Service) ScaleApp(ctx context.Context, tenantID, appID string, replicas int) (App, error) {
	if err := validateReplicas(replicas); err != nil {
		return App{}, err
	}
	app, workloadID, err := s.runningApp(ctx, tenantID, appID)
	if err != nil {
		return App{}, err
	}
	if err = s.orchestrator.Scale(ctx, workloadID, replicas); err != nil {
		return App{}, apperr.Wrap(err, apperr.CodeUnavailable, 503, "the app could not be scaled")
	}
	app.Replicas = replicas
	app.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateApp(ctx, app); err != nil {
		return App{}, err
	}
	s.audit(ctx, "hosting.app.scale", tenantID, appID, "replicas", replicas)
	return app, nil
}

// AppLogs returns the tail of the current workload's output.
func (s *Service) AppLogs(ctx context.Context, tenantID, appID string, lines int) ([]string, error) {
	if lines <= 0 || lines > maxLogLines {
		lines = maxLogLines
	}
	_, workloadID, err := s.runningApp(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	out, err := s.orchestrator.Logs(ctx, workloadID, lines)
	if err != nil {
		return nil, apperr.Wrap(err, apperr.CodeUnavailable, 503, "the logs are not available")
	}
	for i := range out {
		out[i] = s.sanitizeText(out[i])
	}
	return out, nil
}

// CleanupReleases prunes superseded releases past the retention count across
// every app. It is the body of the deploy-cleanup scheduler task.
func (s *Service) CleanupReleases(ctx context.Context) error {
	apps, err := s.store.ListAllApps(ctx)
	if err != nil {
		return err
	}
	for _, app := range apps {
		if err = s.cleanupApp(ctx, app); err != nil {
			return err
		}
	}
	return nil
}

// cleanupApp keeps the newest retained releases of one app and deletes the
// rest, along with their files.
func (s *Service) cleanupApp(ctx context.Context, app App) error {
	releases, err := s.store.ListReleases(ctx, app.TenantID, app.ID)
	if err != nil {
		return err
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Number > releases[j].Number })
	kept := 0
	for _, r := range releases {
		if r.ID == app.ReleaseID || r.State == ReleaseDeployed {
			continue
		}
		kept++
		if kept <= s.keepReleases {
			continue
		}
		dir, dirErr := s.releaseDir(app, r.Number)
		if dirErr != nil {
			return dirErr
		}
		if err = os.RemoveAll(dir); err != nil {
			return internalErr(err, "an old release could not be removed")
		}
		if err = s.store.DeleteRelease(ctx, app.TenantID, r.ID); err != nil {
			return err
		}
	}
	return nil
}

// failRelease records a failed deploy on both the release and the app and
// returns an API-safe error.
func (s *Service) failRelease(ctx context.Context, app App, release Release, cause error, message string) (Release, error) {
	now := s.now().UTC()
	release.State = ReleaseFailed
	release.UpdatedAt = now
	if err := s.store.UpdateRelease(ctx, release); err != nil {
		return Release{}, err
	}
	app.State = AppFailed
	app.UpdatedAt = now
	if err := s.store.UpdateApp(ctx, app); err != nil {
		return Release{}, err
	}
	s.audit(ctx, "hosting.app.deploy.failed", app.TenantID, app.ID, "release", release.Number)
	return Release{}, apperr.Wrap(cause, apperr.CodeUnavailable, 503, message)
}

// supersede moves every previously deployed release of the set to state.
func (s *Service) supersede(ctx context.Context, tenantID string, releases []Release, state string) error {
	now := s.now().UTC()
	for _, r := range releases {
		if r.State != ReleaseDeployed {
			continue
		}
		r.State = state
		r.UpdatedAt = now
		if err := s.store.UpdateRelease(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// deployEnv decrypts the app environment for a deploy. The plaintext lives
// only in the returned map and is never logged or persisted.
func (s *Service) deployEnv(ctx context.Context, tenantID, appID string) (map[string]string, error) {
	entries, err := s.store.ListEnv(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(entries))
	for _, e := range entries {
		if !e.Secret {
			env[e.Key] = e.Value
			continue
		}
		plain, decErr := security.Decrypt(s.key, e.Encrypted)
		if decErr != nil {
			return nil, internalErr(decErr, "a stored secret could not be read")
		}
		env[e.Key] = string(plain)
	}
	return env, nil
}

// runningApp loads an app that has a workload the orchestrator can act on.
func (s *Service) runningApp(ctx context.Context, tenantID, appID string) (App, string, error) {
	app, err := s.GetApp(ctx, tenantID, appID)
	if err != nil {
		return App{}, "", err
	}
	if s.orchestrator == nil {
		return App{}, "", apperr.New(apperr.CodeUnavailable, 503, "the orchestrator is not configured")
	}
	if app.WorkloadID == "" {
		return App{}, "", apperr.New(apperr.CodeConflict, 409, "the app has not been deployed yet")
	}
	return app, app.WorkloadID, nil
}

// appDir resolves the tenant-scoped directory of an app.
func (s *Service) appDir(app App) (string, error) {
	if err := ValidateName("name", app.Name); err != nil {
		return "", err
	}
	return s.tenantDir(DirApps, app.TenantID, app.Name)
}

// appReleasesDir resolves the release parent directory of an app.
func (s *Service) appReleasesDir(app App) (string, error) {
	if err := ValidateName("name", app.Name); err != nil {
		return "", err
	}
	return s.tenantDir(DirApps, app.TenantID, path(app.Name, appReleaseDir))
}

// releaseDir resolves the directory of one numbered release.
func (s *Service) releaseDir(app App, number int64) (string, error) {
	if err := ValidateName("name", app.Name); err != nil {
		return "", err
	}
	if number <= 0 {
		return "", invalid("release", "has an invalid number")
	}
	return s.tenantDir(DirApps, app.TenantID, path(app.Name, releaseRelPath(number)))
}

// releaseRelPath is the app-relative path of a numbered release.
func releaseRelPath(number int64) string {
	return path(appReleaseDir, "r"+strconv.FormatInt(number, 10))
}

// sanitizeLog trims build output to the retention ceiling and strips host
// paths, so a release log can be shown to a tenant safely.
func (s *Service) sanitizeLog(out []byte) string {
	text := string(out)
	if len(text) > maxReleaseLog {
		text = text[len(text)-maxReleaseLog:]
	}
	return s.sanitizeText(text)
}

// sanitizeText removes the hosting root from a line of service output.
func (s *Service) sanitizeText(v string) string {
	if s.root == "" {
		return v
	}
	return strings.ReplaceAll(v, s.root+"/", "")
}

// normalizeCommand validates every argv entry of a start command.
func normalizeCommand(command []string) ([]string, error) {
	if len(command) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(command))
	for _, token := range command {
		if err := ValidateCommandToken(token); err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, nil
}

// splitCommand rebuilds an argv slice from its stored form. Tokens cannot
// contain whitespace, so the split is lossless.
func splitCommand(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.Fields(v)
}

// validatePort bounds a TCP port.
func validatePort(field string, port int) error {
	if port < 1 || port > maxUint16 {
		return invalid(field, "must be between 1 and 65535")
	}
	return nil
}

// validateReplicas bounds an instance count.
func validateReplicas(replicas int) error {
	if replicas < 0 || replicas > maxReplicas {
		return invalid("replicas", "must be between 0 and 64")
	}
	return nil
}
