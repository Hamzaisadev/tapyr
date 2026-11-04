package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/kentrow/tapyr/internal/api"
	"github.com/kentrow/tapyr/internal/auth"
	"github.com/kentrow/tapyr/internal/auth/plugins"
	"github.com/kentrow/tapyr/internal/budget"
	"github.com/kentrow/tapyr/internal/budget/planner"
	"github.com/kentrow/tapyr/internal/buildinfo"
	"github.com/kentrow/tapyr/internal/collect"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/mapping"
	"github.com/kentrow/tapyr/internal/model"
	"github.com/kentrow/tapyr/internal/output"
	"github.com/kentrow/tapyr/internal/output/otlp"
	"github.com/kentrow/tapyr/internal/output/prometheus"
	"github.com/kentrow/tapyr/internal/output/remotewrite"
	"github.com/kentrow/tapyr/internal/runtime"
	"github.com/kentrow/tapyr/internal/scheduler"
	"github.com/kentrow/tapyr/internal/secret"
	"github.com/kentrow/tapyr/internal/selfobs"
	"github.com/kentrow/tapyr/internal/series"
	"github.com/kentrow/tapyr/internal/state"
	internalui "github.com/kentrow/tapyr/internal/ui"
	"github.com/kentrow/tapyr/ui"
)

// runsShutdownTimeout bounds the wait for runs in flight on shutdown and
// on reload.
const runsShutdownTimeout = 30 * time.Second

// saltKey is the metadata key of the credential salt.
const saltKey = "credential_salt"

// instanceIDKey is the metadata key of the instance identifier
// (service.instance.id of OTLP).
const instanceIDKey = "instance_id"

func newRunCmd(e *env, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the collector",
		Long: "Load and compile the configuration, then collect: run every job at its\n" +
			"interval through the budget engine and expose the samples on /metrics.\n" +
			"SIGHUP reloads the configuration; SIGINT and SIGTERM shut down gracefully.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := loadConfig(e, flags)
			if err != nil {
				return err
			}
			// Compile once to report every problem (secrets included) before
			// touching the state store, then again with the credential salt.
			// Secrets are compiled twice at startup: short ones are counted
			// once each.
			redactor, short := selfobs.NewRedactor(), map[string]bool{}
			var smu sync.Mutex
			secrets := &config.SecretSource{LookupEnv: e.lookupEnv, ReadFile: os.ReadFile, OnResolve: func(v string) {
				if !redactor.Add(v) {
					smu.Lock()
					short[v] = true
					smu.Unlock()
				}
			}}
			check, err := offlineAuth(e.clock)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			if _, err := compileGraph(e, cfg, runtime.Options{Secrets: secrets, Auth: check}); err != nil {
				return err
			}
			store, err := state.OpenSQLite(ctx, cfg.Settings.Spec.State.Path, false)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			defer store.Close()
			salt, err := credentialSalt(ctx, store)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			instance, err := instanceID(ctx, store)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			logger, err := settingsLogger(e, flags, &cfg.Settings.Spec.Log, redactor)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			smu.Lock()
			if len(short) > 0 {
				logger.Warn("secrets too short to be redacted from logs", "component", "run", "secrets", len(short),
					"min_length", selfobs.MinRedactedLength)
			}
			smu.Unlock()
			c := &collector{
				e: e, flags: flags, store: store, logger: logger, secrets: secrets, salt: salt, instance: instance,
				client: &http.Client{}, series: series.New(), accum: series.NewAccumulators(), outputs: map[string]*runningOutput{}, mux: http.NewServeMux(),
				events: api.NewBroker(e.clock), debug: newDebugCaptures(redactor.Redact), circuits: map[string]int{}, redact: redactor.Redact,
			}
			c.metrics = selfobs.NewMetrics(buildinfo.Get(), c.selfState)
			if c.tokens, err = auth.NewManager(auth.ManagerOptions{
				Clock: e.clock, Logger: logger, Store: store, StateKey: e.getenv("TAPYR_STATE_KEY"), Observer: c.metrics,
				OnToken: func(token string) { redactor.Add(token) }, Plugins: plugins.Builtin(),
			}); err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			graph, err := c.compile(cfg)
			if err != nil {
				return err
			}
			return c.run(ctx, cfg, graph)
		},
	}
}

// credentialSalt returns the salt of credential keys, created on first use.
func credentialSalt(ctx context.Context, store state.Store) ([]byte, error) {
	v, ok, err := store.Meta(ctx, saltKey)
	if err != nil {
		return nil, err
	}
	if ok {
		return hex.DecodeString(v)
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return salt, store.SetMeta(ctx, saltKey, hex.EncodeToString(salt))
}

// instanceID returns the identifier of this Tapyr instance, created on
// first use.
func instanceID(ctx context.Context, store state.Store) (string, error) {
	v, ok, err := store.Meta(ctx, instanceIDKey)
	if err != nil || ok {
		return v, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	v = hex.EncodeToString(id)
	return v, store.SetMeta(ctx, instanceIDKey, v)
}

// settingsLogger applies the Settings log configuration where neither a
// flag nor an environment variable sets it.
func settingsLogger(e *env, flags *globalFlags, s *config.LogSettings, redactor *selfobs.Redactor) (*slog.Logger, error) {
	level := firstNonEmpty(flags.logLevel, e.getenv("TAPYR_LOG_LEVEL"), s.Level)
	format := firstNonEmpty(flags.logFormat, e.getenv("TAPYR_LOG_FORMAT"), s.Format)
	l, err := selfobs.ParseLogLevel(level)
	if err != nil {
		return nil, err
	}
	f, err := selfobs.ParseLogFormat(format)
	if err != nil {
		return nil, err
	}
	return selfobs.NewLogger(e.stderr, selfobs.LogOptions{Level: l, Format: f, Clock: e.clock, Redactor: redactor}), nil
}

// collector is the process of tapyr run: what outlives a configuration
// reload (state store, series registry, outputs, token sessions, HTTP
// server) and the current generation, the pipeline of one compiled graph.
type collector struct {
	e      *env
	flags  *globalFlags
	store  state.Store
	logger *slog.Logger
	// redact removes the secrets and tokens from a text that leaves the
	// process (run errors, reload errors), as the log handler does.
	redact  func(string) string
	tokens  *auth.Manager
	secrets *config.SecretSource
	salt    []byte
	// instance is the persisted identifier of this instance.
	instance string
	// client is shared by the generations, so that a reload does not leave
	// idle connections behind.
	client *http.Client
	// tlsClients are the clients of the accounts with TLS settings, by
	// account, kept across reloads while their settings do not change.
	tlsMu      sync.Mutex
	tlsClients map[string]*accountTLSClient
	series     *series.Registry
	accum      *series.Accumulators
	mux        *http.ServeMux
	metrics    *selfobs.Metrics
	// runs numbers the runs of every generation.
	runs atomic.Uint64
	// heartbeat is the last tick of the flush loop (unix nanoseconds on the
	// injected clock), for /healthz.
	heartbeat atomic.Int64
	// outputsStarted is set once the outputs run, for /readyz.
	outputsStarted atomic.Bool

	// events streams the events of the collector to the UI.
	events *api.Broker
	// debug keeps the last runs for the live debug endpoint.
	debug *debugCaptures
	// circuits are the last circuit states seen, by account and target,
	// to publish their changes.
	cmu      sync.Mutex
	circuits map[string]int

	// smu guards what the status API reports of the process.
	smu        sync.Mutex
	started    time.Time
	lastReload api.Reload
	configHash string

	// settings are those of the running configuration (under gmu);
	// selfChanged is closed when they change.
	settings    *config.SettingsSpec
	selfChanged chan struct{}

	omu     sync.RWMutex
	outputs map[string]*runningOutput
	prom    *prometheus.Output

	gmu sync.Mutex
	gen *generation
}

// runningOutput is an output and its sender loop.
type runningOutput struct {
	out   output.Output
	spec  config.OutputSpec
	creds *runtime.OutputCredentials
	// dedup tells whether the output deduplicates samples by source
	// timestamp (push outputs).
	dedup  bool
	cancel context.CancelFunc
	done   chan struct{}
}

// compile compiles a configuration for this process: with the credential
// salt and the shared token sessions.
func (c *collector) compile(cfg *config.Config) (*runtime.Graph, error) {
	return compileGraph(c.e, cfg, runtime.Options{Secrets: c.secrets, CredentialSalt: c.salt, Auth: c.tokens})
}

// run collects until ctx is done, reloading the configuration on request.
func (c *collector) run(ctx context.Context, cfg *config.Config, graph *runtime.Graph) error {
	entries, err := c.store.LoadLedger(ctx)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	snaps, err := c.store.LoadSeries(ctx)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	if skipped := c.series.Restore(snaps); skipped > 0 {
		c.logger.Warn("invalid series keys skipped in the state", "component", "state", "skipped", skipped)
	}
	accs, err := c.store.LoadAccumulators(ctx)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	c.accum.Restore(accs)
	c.settings, c.selfChanged = graph.Settings, make(chan struct{})
	c.started = c.e.clock.Now()
	c.heartbeat.Store(c.started.UnixNano())
	c.mux.HandleFunc("GET /metrics", c.serveMetrics)
	c.mux.Handle("GET /metrics/self", c.metrics.Handler())
	c.mux.HandleFunc("GET /healthz", c.serveHealth)
	c.mux.HandleFunc("GET /readyz", c.serveReady)
	c.mux.Handle("/api/", api.Handler(statusSource{c}, c.events))
	if e := graph.Settings.Server.UI.Enabled; e == nil || *e {
		c.mux.Handle("GET "+internalui.Prefix, internalui.Handler(ui.Dist()))
		c.mux.Handle("GET /{$}", http.RedirectHandler(internalui.Prefix, http.StatusFound))
	}
	c.debug.configure(graph.Settings.Server.Debug.Enabled, graph.Settings.Server.Debug.MaxBodyBytes)
	ln, err := c.e.listen(ctx, "tcp", graph.Settings.Server.Listen)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	c.applyOutputs(ctx, graph)
	c.outputsStarted.Store(true)
	g, err := c.newGeneration(cfg, graph)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	restored := g.engine.Restore(entries)
	c.start(ctx, g, nil)
	c.metrics.ConfigLoaded(c.e.clock.Now(), true)
	c.recordLoad(cfg, nil)
	active, dynamic := g.disc.counts()
	c.logger.Info("collector started", "component", "run", "address", ln.Addr().String(),
		"instances", active, "discovered_instances", dynamic, "outputs", len(c.outputs), "ledger_entries", restored)

	loops := sync.WaitGroup{}
	loops.Go(func() { c.flushLoop(ctx) })
	loops.Go(func() { c.reloadLoop(ctx) })
	loops.Go(func() { c.selfPushLoop(ctx) })
	handler, err := c.serverHandler(graph.Settings.Server)
	if err != nil {
		return &exitError{code: exitFailure, err: err}
	}
	certFile, keyFile := "", ""
	if tls := graph.Settings.Server.TLS; tls != nil {
		certFile, keyFile = tls.CertFile, tls.KeyFile
	}
	serveErr := serveTLS(ctx, ln, handler, certFile, keyFile)
	loops.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runsShutdownTimeout)
	defer cancel()
	c.current().stop(shutdownCtx, "runs canceled on shutdown")
	c.tokens.Close(shutdownCtx)
	c.stopOutputs(shutdownCtx, slices.Collect(maps.Keys(c.outputs)))
	c.flushLedger(shutdownCtx)
	c.logger.Info("collector stopped", "component", "run")
	if serveErr != nil {
		return &exitError{code: exitFailure, err: serveErr}
	}
	return nil
}

// serverHandler wraps the mux with the basic auth of the settings.
func (c *collector) serverHandler(s config.ServerSettings) (http.Handler, error) {
	ba := s.BasicAuth
	if ba == nil {
		return c.mux, nil
	}
	user, err := secret.Resolve(ba.Username, *c.secrets)
	if err != nil {
		return nil, fmt.Errorf("server.basicAuth.username: %w", err)
	}
	pass, err := secret.Resolve(ba.Password, *c.secrets)
	if err != nil {
		return nil, fmt.Errorf("server.basicAuth.password: %w", err)
	}
	return basicAuth(c.mux, user.Reveal(), pass.Reveal(), ba.ProtectMetrics), nil
}

// serveMetrics serves the exposition of the prometheus output, if any.
func (c *collector) serveMetrics(w http.ResponseWriter, r *http.Request) {
	c.omu.RLock()
	prom := c.prom
	c.omu.RUnlock()
	if prom == nil {
		http.NotFound(w, r)
		return
	}
	prom.Handler().ServeHTTP(w, r)
}

// applyOutputs starts the outputs of a graph: an output whose spec did not
// change keeps running (the pull exposition keeps serving), others start
// afresh. Outputs absent from the graph are stopped.
func (c *collector) applyOutputs(ctx context.Context, graph *runtime.Graph) {
	var stale []string
	next := map[string]*runningOutput{}
	c.omu.RLock()
	for _, o := range graph.Outputs {
		name := string(o.Metadata.Name)
		if prev, ok := c.outputs[name]; ok && reflect.DeepEqual(prev.spec, o.Spec) &&
			sameCredentials(prev.creds, graph.OutputCredentials[name]) {
			next[name] = prev
		}
	}
	for name := range c.outputs {
		if _, kept := next[name]; !kept {
			stale = append(stale, name)
		}
	}
	c.omu.RUnlock()
	c.stopOutputs(ctx, stale)

	c.omu.Lock()
	defer c.omu.Unlock()
	c.prom = nil
	for _, o := range graph.Outputs {
		name := string(o.Metadata.Name)
		ro, ok := next[name]
		if !ok {
			out, err := c.newOutput(name, o.Spec, graph.OutputCredentials[name])
			if err != nil {
				// The compiler validated the output: this is not expected.
				c.logger.Error("output not started", "component", "output", "output", name, "error", err)
				continue
			}
			octx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			ro = &runningOutput{
				out: out, spec: o.Spec, creds: graph.OutputCredentials[name], dedup: o.Spec.Type != config.OutputPrometheus,
				cancel: cancel, done: make(chan struct{}),
			}
			go func() {
				defer close(ro.done)
				if err := out.Run(octx); err != nil {
					c.logger.Error("output stopped", "component", "output", "output", name, "error", err)
				}
			}()
			next[name] = ro
		}
		if prom, isProm := ro.out.(*prometheus.Output); isProm {
			c.prom = prom
		}
	}
	c.outputs = next
}

// newOutput builds an output of the configuration.
func (c *collector) newOutput(name string, spec config.OutputSpec, creds *runtime.OutputCredentials) (output.Output, error) {
	switch spec.Type {
	case config.OutputRemoteWrite:
		return remotewrite.New(remotewrite.Options{
			Name: name, Spec: spec, Credentials: creds, Client: c.client, Clock: c.e.clock, Logger: c.logger,
			Observer: c.metrics, UserAgent: userAgent(),
		}), nil
	case config.OutputOTLP:
		return otlp.New(otlp.Options{
			Name: name, Spec: spec, Credentials: creds, Client: c.client, Clock: c.e.clock, Logger: c.logger,
			Observer: c.metrics, Version: buildinfo.Get().Version, InstanceID: c.instance,
		})
	default:
		return prometheus.New(name, spec.Prometheus.ExposeTimestamps, c.logger), nil
	}
}

// userAgent is the User-Agent of the requests of the push outputs.
func userAgent() string { return "tapyr/" + buildinfo.Get().Version }

// sameCredentials reports whether two resolved credentials are equal, so
// that an output whose secrets changed starts afresh on reload.
func sameCredentials(a, b *runtime.OutputCredentials) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Username.Reveal() != b.Username.Reveal() || a.Password.Reveal() != b.Password.Reveal() || len(a.Headers) != len(b.Headers) {
		return false
	}
	for k, v := range a.Headers {
		if w, ok := b.Headers[k]; !ok || w.Reveal() != v.Reveal() {
			return false
		}
	}
	return true
}

// stopOutputs flushes and stops outputs that are no longer in use.
func (c *collector) stopOutputs(ctx context.Context, names []string) {
	c.omu.Lock()
	stopping := make([]*runningOutput, 0, len(names))
	for _, name := range names {
		if ro, ok := c.outputs[name]; ok {
			stopping = append(stopping, ro)
			delete(c.outputs, name)
		}
	}
	c.omu.Unlock()
	for _, ro := range stopping {
		if err := ro.out.Flush(ctx); err != nil {
			c.logger.Warn("flushing output", "component", "output", "output", ro.out.Name(), "error", err)
		}
		ro.cancel()
		<-ro.done
	}
}

// generation is the pipeline of one compiled graph: budget engine,
// executor, discovery and scheduler.
type generation struct {
	c        *collector
	cfg      *config.Config
	graph    *runtime.Graph
	engine   *budget.Engine
	executor *collect.Executor
	disc     *discovery
	// headers are the response headers kept by the live debug endpoint, by
	// provider.
	headers map[string][]string
	sched   *scheduler.Scheduler
	cancel  context.CancelFunc
	loops   sync.WaitGroup

	pmu    sync.Mutex
	plan   *planner.Result
	planAt time.Time
	// planSig summarizes the plan applied, to publish its changes.
	planSig string
}

// newGeneration builds the pipeline of a graph; it is started by start.
func (c *collector) newGeneration(cfg *config.Config, graph *runtime.Graph) (*generation, error) {
	engine, err := budget.NewEngine(budget.Config{
		Providers: cfg.Providers, Accounts: cfg.Accounts,
		RetryReserve: graph.Settings.Budget.RetryReserve.Fraction(),
		Breaker:      graph.Settings.CircuitBreaker,
		Observer:     c.metrics,
	}, c.e.clock, c.logger)
	if err != nil {
		return nil, err
	}
	g := &generation{c: c, cfg: cfg, graph: graph, engine: engine, headers: keptHeaders(cfg.Providers)}
	for p, hs := range g.headers {
		g.headers[p] = append(hs, debugHeaders...)
	}
	g.disc = newDiscovery(graph.Jobs, c.store, func(instance string) {
		c.retire(instance, c.series.Remove(instance))
	}, c.logger)
	g.disc.observe = c.e.onInstances
	clients := c.accountClients(graph)
	g.executor = collect.New(collect.Options{
		Client:                c.client,
		ClientFor:             func(account string) *http.Client { return clients[account] },
		Budget:                engine,
		Clock:                 c.e.clock,
		Logger:                c.logger,
		MaxConcurrentRequests: graph.Settings.Collection.MaxConcurrentRequests,
		Retry:                 graph.Settings.Retry,
		Observer:              c.metrics,
	})
	return g, nil
}

// accountTLSClient is the HTTP client of an account with TLS settings.
type accountTLSClient struct {
	fingerprint string
	client      *http.Client
}

// accountClients returns the clients of the accounts of a graph that have
// TLS settings, reusing those whose settings did not change, and logs the
// accounts that disable certificate verification.
func (c *collector) accountClients(graph *runtime.Graph) map[string]*http.Client {
	c.tlsMu.Lock()
	defer c.tlsMu.Unlock()
	next := map[string]*accountTLSClient{}
	out := map[string]*http.Client{}
	for _, account := range slices.Sorted(maps.Keys(graph.TLS)) {
		t := graph.TLS[account]
		if t.Insecure {
			c.logger.Warn("TLS certificate verification is disabled (insecureSkipVerify)", "component", "run", "account", account)
		}
		tc, ok := c.tlsClients[account]
		if !ok || tc.fingerprint != t.Fingerprint {
			tc = &accountTLSClient{fingerprint: t.Fingerprint, client: tlsClient(t)}
		}
		next[account], out[account] = tc, tc.client
	}
	for account, tc := range c.tlsClients {
		if next[account] != tc {
			tc.client.CloseIdleConnections()
		}
	}
	c.tlsClients = next
	return out
}

// start schedules the instances of a generation and makes it current.
// resume holds the next due times of the instances of the previous
// generation, which keep their schedule.
func (c *collector) start(ctx context.Context, g *generation, resume map[string]time.Time) {
	opts := scheduler.Options{
		Clock: c.e.clock, Jitter: g.graph.Settings.Collection.Jitter.Fraction(), Logger: c.logger, Runner: g.runJob,
		OnRun: c.e.onRun, Sequence: &c.runs,
	}
	if resume != nil {
		opts.Resume = func(instance string) (time.Time, bool) {
			t, ok := resume[instance]
			return t, ok
		}
	}
	g.sched = scheduler.New(nil, opts)
	g.disc.attach(ctx, g.sched, c.e.clock.Now())
	g.replan(ctx)
	c.tokens.SetSender(g.executor)
	c.gmu.Lock()
	c.gen = g
	c.gmu.Unlock()
	gctx, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	g.loops.Go(func() { g.sched.Run(gctx) })
	g.loops.Go(func() { g.replanLoop(gctx) })
}

// stop stops scheduling and waits for the runs in flight, canceled when ctx
// ends first.
func (g *generation) stop(ctx context.Context, canceledMsg string) {
	g.cancel()
	g.loops.Wait()
	if err := g.sched.Wait(ctx); err != nil {
		g.c.logger.Warn(canceledMsg, "component", "run", "error", err)
	}
}

func (c *collector) current() *generation {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	return c.gen
}

// reloadLoop reloads the configuration on every request (SIGHUP) until ctx
// is done.
func (c *collector) reloadLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.e.reload:
			err := c.reload(ctx)
			c.metrics.ConfigLoaded(c.e.clock.Now(), err == nil)
			if err != nil {
				c.recordLoad(nil, err)
				c.logger.Error("configuration reload rejected: the running configuration is kept", "component", "run", "error", err)
				c.events.Publish(api.EventConfigReloaded, map[string]any{"result": "failed", "error": c.redact(err.Error())})
			} else {
				c.events.Publish(api.EventConfigReloaded, map[string]any{"result": "success"})
			}
			if c.e.onReload != nil {
				c.e.onReload(err)
			}
		}
	}
}

// reload loads and compiles the configuration, then swaps generations: the
// old one stops scheduling and finishes its runs, the ledger moves to the
// new engine, unchanged outputs keep running, instances that disappeared
// are retired and the others keep their schedule.
func (c *collector) reload(ctx context.Context) error {
	cfg, err := loadConfig(c.e, c.flags)
	if err != nil {
		return err
	}
	graph, err := c.compile(cfg)
	if err != nil {
		return err
	}
	next, err := c.newGeneration(cfg, graph)
	if err != nil {
		return err
	}
	c.warnRestartSettings(graph.Settings)

	old := c.current()
	stopCtx, cancel := context.WithTimeout(ctx, runsShutdownTimeout)
	defer cancel()
	old.stop(stopCtx, "runs canceled on reload")
	before := old.disc.activeKeys()
	resume := old.sched.Next()
	next.engine.Restore(old.engine.Export())
	c.applyOutputs(ctx, graph)
	c.start(ctx, next, resume)
	after := next.disc.activeKeys()
	retired := 0
	for key := range before {
		if !after[key] {
			c.retire(key, c.series.Remove(key))
			retired++
		}
	}
	c.recordLoad(cfg, nil)
	c.debug.configure(graph.Settings.Server.Debug.Enabled, graph.Settings.Server.Debug.MaxBodyBytes)
	c.gmu.Lock()
	c.settings = graph.Settings
	close(c.selfChanged)
	c.selfChanged = make(chan struct{})
	c.gmu.Unlock()
	active, dynamic := next.disc.counts()
	c.logger.Info("configuration reloaded", "component", "run", "instances", active, "discovered_instances", dynamic,
		"retired_instances", retired, "outputs", len(c.outputs))
	return nil
}

// warnRestartSettings logs the settings changes that only a restart applies.
func (c *collector) warnRestartSettings(next *config.SettingsSpec) {
	for name, changed := range map[string]bool{
		"server":     !reflect.DeepEqual(c.settings.Server, next.Server),
		"state.path": c.settings.State.Path != next.State.Path,
		"log":        c.settings.Log != next.Log,
	} {
		if changed {
			c.logger.Warn("settings changed that take effect after a restart", "component", "run", "settings", name)
		}
	}
}

// flushLoop persists the ledger and the series, and expires stale
// inventories and series, every budget.FlushInterval until ctx is done.
func (c *collector) flushLoop(ctx context.Context) {
	ticker := c.e.clock.NewTicker(budget.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			// A shutdown does not interrupt a flush: the final one follows.
			fctx := context.WithoutCancel(ctx)
			now := c.e.clock.Now()
			c.heartbeat.Store(now.UnixNano())
			g := c.current()
			g.disc.expire(fctx, now)
			c.expireSeries(g, now)
			c.flushLedger(fctx)
		}
	}
}

// replanLoop recomputes the effective intervals every replanInterval,
// whenever a response header changes the max of a quota and whenever
// discovery adds or removes instances, with the remaining budget and time of
// the current windows.
func (g *generation) replanLoop(ctx context.Context) {
	var tick <-chan time.Time
	if interval := g.graph.Settings.Budget.ReplanInterval.Std(); interval > 0 {
		ticker := g.c.e.clock.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		case <-g.engine.LimitLearned():
		case <-g.disc.changed:
		}
		g.replan(ctx)
	}
}

// replan applies an online plan to the scheduler.
func (g *generation) replan(ctx context.Context) {
	logger := g.c.logger
	jobs, err := planInputs(ctx, g.disc.planEntries(), g.engine, g.c.store)
	if err != nil {
		logger.Warn("planning", "component", "planner", "error", err)
		return
	}
	res := planner.Plan(jobs, planner.Options{MaxStretch: g.graph.Settings.Budget.MaxStretch, Online: true, Now: g.c.e.clock.Now()})
	var sig strings.Builder
	for _, j := range res.Jobs {
		fmt.Fprintf(&sig, "%s=%s;", j.Name, j.Effective)
	}
	for _, s := range res.Scopes {
		fmt.Fprintf(&sig, "%s=%t;", s.ID, s.Feasible)
	}
	g.pmu.Lock()
	g.plan, g.planAt = res, g.c.e.clock.Now()
	changed := sig.String() != g.planSig
	g.planSig = sig.String()
	g.pmu.Unlock()
	if changed {
		g.c.events.Publish(api.EventPlanChanged, map[string]any{"feasible": res.Feasible})
	}
	for _, j := range res.Jobs {
		if prev := g.sched.Interval(j.Name); prev != j.Effective {
			g.sched.SetInterval(j.Name, j.Effective)
			logger.Info("effective interval changed", "component", "planner", "provider", j.Provider, "account", j.Account,
				"job", j.Name, "desired", j.Desired.String(), "previous", prev.String(), "effective", j.Effective.String(),
				"reason", j.Describe())
		}
	}
	for _, s := range res.Scopes {
		if !s.Feasible {
			logger.Warn("plan infeasible: critical jobs alone exceed the remaining quota", "component", "planner",
				"limit", s.Limit, "scope", s.Key, "budget", s.Budget, "projected", s.Projected)
		}
	}
}

// flushLedger persists the ledger of the current engine and the series
// registry.
func (c *collector) flushLedger(ctx context.Context) {
	started := c.e.clock.Now()
	err := c.store.SaveLedger(ctx, c.current().engine.Export())
	if err != nil {
		c.logger.Warn("persisting the ledger", "component", "state", "error", err)
	}
	if changed, removed := c.series.Snapshot(); len(changed)+len(removed) > 0 {
		if serr := c.store.SaveSeries(ctx, changed, removed); serr != nil {
			c.logger.Warn("persisting the series", "component", "state", "error", serr)
			err = errors.Join(err, serr)
		}
	}
	if changed, removed := c.accum.Snapshot(); len(changed)+len(removed) > 0 {
		if aerr := c.store.SaveAccumulators(ctx, changed, removed); aerr != nil {
			c.logger.Warn("persisting the reconstructed counters", "component", "state", "error", aerr)
			err = errors.Join(err, aerr)
		}
	}
	c.metrics.StateFlushed(c.e.clock.Since(started), err)
}

// expireSeries withdraws the series of the instances without a successful
// run within their staleAfter, and of the instances no longer scheduled.
func (c *collector) expireSeries(g *generation, now time.Time) {
	g.disc.do(func(active map[string]*runtime.Job) {
		expired := c.series.Expired(now, func(instance string) (time.Duration, bool) {
			j, ok := active[instance]
			if !ok {
				return 0, false
			}
			return g.disc.staleAfter(j), true
		})
		for _, instance := range expired {
			j, ok := active[instance]
			if !ok {
				c.retire(instance, c.series.Remove(instance))
				continue
			}
			stale := c.series.Expire(instance)
			c.logger.Warn("series withdrawn: no successful run within staleAfter", "component", "series", "provider", j.Provider,
				"account", j.Account, "job", j.Name, "instance", instance, "series", len(stale),
				"stale_after", g.disc.staleAfter(j).String())
			c.retire(instance, stale)
		}
	})
}

// retire withdraws the series of an instance from every output; its
// reconstructed counters start again.
func (c *collector) retire(instance string, stale []model.Series) {
	c.accum.Remove(instance)
	c.debug.remove(instance)
	c.omu.RLock()
	defer c.omu.RUnlock()
	for _, ro := range c.outputs {
		ro.out.Retire(instance, stale)
	}
}

// runJob executes one run: requests, mapping, then outputs and history.
func (g *generation) runJob(ctx context.Context, run scheduler.Run) {
	c := g.c
	job := run.Job
	log := c.logger.With("component", "run", "provider", job.Provider, "account", job.Account, "job", job.Name,
		"instance", job.Instance, "run_id", run.ID)
	started := c.e.clock.Now()
	record := state.RunRecord{Job: job.Name, Instance: job.Instance, RunID: run.ID, Started: started, Result: "success"}
	obs := selfobs.Run{Provider: job.Provider, Account: job.Account, Job: job.Name}
	var res *collect.Result
	var samples []model.Sample
	defer func() {
		record.Ended = c.e.clock.Now()
		// The error may quote the response (an excerpt of its body), and an
		// API may echo the credentials it received: it is stored and served
		// redacted.
		record.Error = c.redact(record.Error)
		obs.Result, obs.Duration, obs.Pages = record.Result, record.Ended.Sub(started), record.Pages
		c.metrics.RunFinished(obs)
		if err := c.store.AddRun(context.WithoutCancel(ctx), record); err != nil {
			log.Warn("recording the run", "error", err)
		}
		c.debug.keep(job, run.ID, started, record.Result, record.Error, res, samples, g.headers[job.Provider])
		c.events.Publish(api.EventRunFinished, map[string]any{
			"job": job.Name, "instance": job.Instance, "provider": job.Provider, "account": job.Account, "run_id": run.ID,
			"result": record.Result, "duration_ms": record.Ended.Sub(started).Milliseconds(), "samples": record.Samples,
		})
		c.publishCircuit(g, job)
	}()

	res, err := g.executor.Run(ctx, job, started, run.Deadline)
	if res != nil {
		record.Requests, record.Retries, record.Pages = res.Requests, res.Retries, len(res.Pages)
	}
	if err != nil {
		record.Result, record.Error = failure(err)
		attrs := []any{"result", record.Result, "error", err, "duration_ms", c.e.clock.Since(started).Milliseconds()}
		if ce, ok := errors.AsType[*collect.Error](err); ok {
			attrs = append(attrs, "class", string(ce.Class), "endpoint", ce.Endpoint, "status", ce.Status)
		}
		log.Warn("run failed", attrs...)
		return
	}
	out, err := job.Extract.Evaluate(&mapping.Input{
		Pages: res.Pages, Vars: job.Vars, Account: job.AccountInfo, Target: job.TargetInfo, Now: started, Labels: job.Labels,
	}, job.Limits)
	if err != nil {
		record.Result, record.Error = "failed", err.Error()
		log.Warn("run failed", "result", record.Result, "error", err, "class", string(collect.ClassDecode))
		return
	}
	out.Samples = c.accum.Apply(job.Instance, out.Samples)
	samples = out.Samples
	record.Items, record.Samples = out.Items, len(out.Samples)
	obs.Truncated, obs.MappingErrors = res.Truncated, out.ErrorsByMetric
	for _, mErr := range out.Errors {
		log.Warn("mapping error", "error", mErr)
	}
	if job.Role != config.RoleMetrics {
		if res.Truncated {
			log.Warn("inventory not updated: the run stopped at maxPages", "items", len(out.Inventory))
		} else if err := g.disc.update(ctx, job, out.Inventory, started); err != nil {
			log.Warn("updating the inventory", "error", err)
		}
	}
	deduplicated := 0
	if !g.disc.deliver(job.Instance, func() {
		c.omu.RLock()
		defer c.omu.RUnlock()
		var targets []series.Output
		for _, name := range slices.Sorted(maps.Keys(c.outputs)) {
			if len(job.Outputs) == 0 || slices.Contains(job.Outputs, name) {
				targets = append(targets, series.Output{Name: name, Dedup: c.outputs[name].dedup})
			}
		}
		for name, d := range c.series.Success(job.Instance, out.Samples, targets, started) {
			deduplicated += d.Deduplicated
			c.outputs[name].out.Enqueue(model.Batch{
				Instance: job.Instance, Job: job.Name, RunID: run.ID, Provider: job.Provider, Account: job.Account, At: started,
				Samples: d.Samples, Stale: d.Stale,
			})
		}
	}) {
		log.Info("samples dropped: the instance was removed during the run", "samples", len(out.Samples))
		return
	}
	obs.Deduplicated = deduplicated
	log.Info("run finished", "requests", res.Requests, "retries", res.Retries, "pages", len(res.Pages), "items", out.Items,
		"samples", len(out.Samples), "deduplicated", deduplicated, "mapping_errors", out.MappingErrors, "truncated", res.Truncated,
		"duration_ms", c.e.clock.Since(started).Milliseconds())
}

// publishCircuit publishes the change of the circuit of a job's account
// (and target) seen since its last run.
func (c *collector) publishCircuit(g *generation, job *runtime.Job) {
	state := g.engine.CircuitState(job.Account, job.Target)
	key := job.Account + "/" + job.Target
	c.cmu.Lock()
	prev, seen := c.circuits[key]
	c.circuits[key] = state
	c.cmu.Unlock()
	if seen && prev != state || !seen && state != budget.CircuitClosed {
		c.events.Publish(api.EventCircuitChanged, map[string]any{
			"provider": job.Provider, "account": job.Account, "target": job.Target, "state": circuitName(state),
		})
	}
}

// failure maps a run error to its result and summary.
func failure(err error) (result, summary string) {
	if reason, ok := budget.IsRefusal(err); ok {
		if reason == budget.ReasonCircuitOpen {
			return "skipped_circuit", err.Error()
		}
		return "skipped_budget", err.Error()
	}
	return "failed", err.Error()
}
