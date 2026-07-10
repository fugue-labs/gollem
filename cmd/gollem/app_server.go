package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	appserver "github.com/fugue-labs/gollem/appserver"
	appconfig "github.com/fugue-labs/gollem/appserver/config"
	appmcp "github.com/fugue-labs/gollem/appserver/mcp"
	"github.com/fugue-labs/gollem/appserver/protocol"
	appskills "github.com/fugue-labs/gollem/appserver/skills"
	"github.com/fugue-labs/gollem/appserver/store"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
	"github.com/gorilla/websocket"
)

const defaultAppServerStoreRel = ".gollem/appserver.db"
const defaultAppServerWebSocketPath = "/app-server"

var errAppServerDaemonStopped = errors.New("app-server daemon stop requested")

type appServerFlags struct {
	workDir         string
	storePath       string
	gitRoot         string
	worktreeRoot    string
	provider        string
	modelName       string
	location        string
	project         string
	stdio           bool
	socketPath      string
	websocketAddr   string
	websocketPath   string
	allowMutations  bool
	gitRootExplicit bool
	stdioExplicit   bool
}

func runAppServer() {
	flags, err := parseAppServerFlags(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		printAppServerUsage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serveCLIAppServerTransports(ctx, flags); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "error serving app-server: %v\n", err)
		os.Exit(1)
	}
}

func parseAppServerFlags(args []string) (appServerFlags, error) {
	workDir, _ := os.Getwd()
	flags := appServerFlags{workDir: workDir, stdio: true, websocketPath: defaultAppServerWebSocketPath}

	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--stdio":
			value, consumed, err := parseOptionalBoolFlag(args, i)
			if err != nil {
				return appServerFlags{}, fmt.Errorf("--stdio: %w", err)
			}
			flags.stdio = value
			flags.stdioExplicit = true
			i += consumed
		case strings.HasPrefix(arg, "--stdio="):
			value, err := parseBoolString(strings.TrimPrefix(arg, "--stdio="))
			if err != nil {
				return appServerFlags{}, fmt.Errorf("--stdio: %w", err)
			}
			flags.stdio = value
			flags.stdioExplicit = true
		case arg == "--allow-mutations":
			value, consumed, err := parseOptionalBoolFlag(args, i)
			if err != nil {
				return appServerFlags{}, fmt.Errorf("--allow-mutations: %w", err)
			}
			flags.allowMutations = value
			i += consumed
		case strings.HasPrefix(arg, "--allow-mutations="):
			value, err := parseBoolString(strings.TrimPrefix(arg, "--allow-mutations="))
			if err != nil {
				return appServerFlags{}, fmt.Errorf("--allow-mutations: %w", err)
			}
			flags.allowMutations = value
		case arg == "--workdir":
			value, err := requireServeFlagValue(args, i, "--workdir")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.workDir = strings.TrimSpace(value)
			i++
		case arg == "--store":
			value, err := requireServeFlagValue(args, i, "--store")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.storePath = strings.TrimSpace(value)
			i++
		case arg == "--git-root":
			value, err := requireServeFlagValue(args, i, "--git-root")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.gitRoot = strings.TrimSpace(value)
			flags.gitRootExplicit = true
			i++
		case arg == "--worktree-root":
			value, err := requireServeFlagValue(args, i, "--worktree-root")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.worktreeRoot = strings.TrimSpace(value)
			i++
		case arg == "--provider":
			value, err := requireServeFlagValue(args, i, "--provider")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.provider = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--provider="):
			flags.provider = strings.TrimSpace(strings.TrimPrefix(arg, "--provider="))
		case arg == "--model":
			value, err := requireServeFlagValue(args, i, "--model")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.modelName = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--model="):
			flags.modelName = strings.TrimSpace(strings.TrimPrefix(arg, "--model="))
		case arg == "--location":
			value, err := requireServeFlagValue(args, i, "--location")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.location = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--location="):
			flags.location = strings.TrimSpace(strings.TrimPrefix(arg, "--location="))
		case arg == "--project":
			value, err := requireServeFlagValue(args, i, "--project")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.project = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--project="):
			flags.project = strings.TrimSpace(strings.TrimPrefix(arg, "--project="))
		case arg == "--socket":
			value, err := requireServeFlagValue(args, i, "--socket")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.socketPath = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--socket="):
			flags.socketPath = strings.TrimSpace(strings.TrimPrefix(arg, "--socket="))
		case arg == "--websocket":
			value, err := requireServeFlagValue(args, i, "--websocket")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.websocketAddr = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--websocket="):
			flags.websocketAddr = strings.TrimSpace(strings.TrimPrefix(arg, "--websocket="))
		case arg == "--websocket-path":
			value, err := requireServeFlagValue(args, i, "--websocket-path")
			if err != nil {
				return appServerFlags{}, err
			}
			flags.websocketPath = strings.TrimSpace(value)
			i++
		case strings.HasPrefix(arg, "--websocket-path="):
			flags.websocketPath = strings.TrimSpace(strings.TrimPrefix(arg, "--websocket-path="))
		case arg == "--help" || arg == "-h":
			printAppServerUsage()
			os.Exit(0)
		default:
			return appServerFlags{}, fmt.Errorf("unknown app-server argument %q", arg)
		}
	}
	if strings.TrimSpace(flags.workDir) == "" {
		return appServerFlags{}, errors.New("--workdir must not be empty")
	}
	if flags.websocketPath == "" || !strings.HasPrefix(flags.websocketPath, "/") {
		return appServerFlags{}, errors.New("--websocket-path must start with /")
	}
	if (flags.socketPath != "" || flags.websocketAddr != "") && !flags.stdioExplicit {
		flags.stdio = false
	}
	if !flags.stdio && flags.socketPath == "" && flags.websocketAddr == "" {
		return appServerFlags{}, errors.New("at least one app-server transport must be enabled")
	}
	return flags, nil
}

func newCLIAppServer(flags appServerFlags) (*appserver.Server, func(), error) {
	return newCLIAppServerWithTransport(flags, appServerTransportName(flags))
}

func newCLIAppServerWithTransport(flags appServerFlags, transport string) (*appserver.Server, func(), error) {
	return newCLIAppServerWithRuntimeFactory(flags, transport, appServerRuntimeModelFactory(flags))
}

func newCLIAppServerWithRuntimeFactory(flags appServerFlags, transport string, runtimeFactory appserver.RuntimeModelFactory) (*appserver.Server, func(), error) {
	workDir, err := filepath.Abs(flags.workDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve workdir: %w", err)
	}
	storePath, err := resolveAppServerStorePath(workDir, flags.storePath)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.NewSQLiteStore(storePath)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = st.Close()
	}

	fsOpts := []toolfs.Option{}
	processOpts := []toolprocess.Option{}
	gitOpts := []toolgit.Option{}
	events := appserver.NewEventQueue()
	approvals := appserver.NewApprovalService()
	mcpSvc := appmcp.NewService()
	interactionSvc := appserver.NewInteractionService()
	var server *appserver.Server
	if !flags.allowMutations {
		fsOpts = append(fsOpts, toolfs.WithApproval(approvals.FilesystemApproval))
		processOpts = append(processOpts, toolprocess.WithApproval(approvals.ProcessApproval))
		gitOpts = append(gitOpts, toolgit.WithApproval(approvals.GitApproval))
	}
	processOpts = append(processOpts,
		toolprocess.WithOutputSink(func(ev toolprocess.OutputEvent) {
			if server != nil {
				server.PublishProcessOutput(ev)
				return
			}
			method, params := appserver.ProcessOutputNotification(ev)
			events.Publish(method, params)
		}),
		toolprocess.WithExitSink(func(ev toolprocess.ExitEvent) {
			if server != nil {
				server.PublishProcessExited(ev)
				return
			}
			method, params := appserver.ProcessExitedNotification(ev)
			events.Publish(method, params)
		}),
	)

	fsSvc, err := toolfs.NewService(workDir, fsOpts...)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	storeCleanup := cleanup
	cleanup = func() {
		_ = fsSvc.Close()
		storeCleanup()
	}
	processSvc, err := toolprocess.NewService(workDir, processOpts...)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	memorySvc, err := appserver.NewMemoryService(filepath.Join(workDir, ".gollem", "memories"))
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	var gitSvc *toolgit.Service
	gitRoot := firstNonEmptyAppServer(flags.gitRoot, workDir)
	if flags.worktreeRoot != "" {
		gitOpts = append(gitOpts, toolgit.WithWorktreeRoot(flags.worktreeRoot))
	}
	gitSvc, err = toolgit.NewService(gitRoot, gitOpts...)
	if err != nil && flags.gitRootExplicit {
		cleanup()
		return nil, nil, err
	}
	if err != nil {
		gitSvc = nil
	}
	runtimeTools := appserver.FilesystemRuntimeTools(fsSvc)
	runtimeTools = append(runtimeTools, appserver.ProcessRuntimeTools(processSvc)...)
	if gitSvc != nil {
		runtimeTools = append(runtimeTools, appserver.GitRuntimeTools(gitSvc)...)
	}
	runtimeTools = append(runtimeTools, appserver.MCPRuntimeTools(mcpSvc, approvals)...)
	runtimeTools = append(runtimeTools, appserver.InteractionRuntimeTools(interactionSvc)...)
	runtimeSvc := appserver.NewRuntimeService(
		appserver.WithRuntimeModelFactory(runtimeFactory),
		appserver.WithRuntimeTools(runtimeTools...),
	)

	version := gitCommit
	if version == "" {
		version = "dev"
	}
	opts := []appserver.Option{
		appserver.WithImplementationInfo(protocol.ImplementationInfo{Name: "gollem-appserver", Version: version}),
		appserver.WithDaemonService(appserver.NewDaemonService(
			appserver.WithDaemonName("gollem-appserver"),
			appserver.WithDaemonVersion(version),
			appserver.WithDaemonTransport(transport),
			appserver.WithDaemonWorkDir(workDir),
			appserver.WithDaemonStorePath(storePath),
		)),
		appserver.WithStore(st),
		appserver.WithFilesystem(fsSvc),
		appserver.WithProcess(processSvc),
		appserver.WithConfig(appconfig.NewService(appconfig.WithWorkDir(workDir))),
		appserver.WithMCP(mcpSvc),
		appserver.WithSkills(appskills.NewService(appskills.WithRoots(
			filepath.Join(workDir, ".gollem", "skills"),
			filepath.Join(workDir, ".gollem", "plugins"),
		))),
		appserver.WithMemoryService(memorySvc),
		appserver.WithEventQueue(events),
		appserver.WithApprovalService(approvals),
		appserver.WithInteractionService(interactionSvc),
		appserver.WithRuntimeService(runtimeSvc),
	}
	if gitSvc != nil {
		opts = append(opts, appserver.WithGit(gitSvc))
	}
	server = appserver.NewServer(opts...)
	return server, func() {
		shutdownCLIAppServerRuntime(runtimeSvc)
		cleanup()
	}, nil
}

func shutdownCLIAppServerRuntime(runtimeSvc *appserver.RuntimeService) {
	if runtimeSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runtimeSvc.Shutdown(ctx)
}

func serveCLIAppServerTransports(ctx context.Context, flags appServerFlags) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 3)
	started := 0
	if flags.stdio {
		started++
		go func() {
			server, cleanup, err := newCLIAppServerWithTransport(flags, "stdio")
			if err != nil {
				errCh <- fmt.Errorf("create stdio app-server: %w", err)
				return
			}
			defer cleanup()
			errCh <- appserver.ServeJSONLines(ctx, server, os.Stdin, os.Stdout)
		}()
	}
	if flags.socketPath != "" {
		started++
		go func() {
			errCh <- serveAppServerUnixSocket(ctx, flags)
		}()
	}
	if flags.websocketAddr != "" {
		started++
		go func() {
			errCh <- serveAppServerWebSocket(ctx, flags)
		}()
	}
	if started == 0 {
		return errors.New("no app-server transports enabled")
	}
	err := <-errCh
	cancel()
	if errors.Is(err, errAppServerDaemonStopped) {
		return nil
	}
	return err
}

func appServerTransportName(flags appServerFlags) string {
	var transports []string
	if flags.stdio {
		transports = append(transports, "stdio")
	}
	if flags.socketPath != "" {
		transports = append(transports, "socket")
	}
	if flags.websocketAddr != "" {
		transports = append(transports, "websocket")
	}
	if len(transports) == 0 {
		return "unknown"
	}
	if len(transports) == 1 {
		return transports[0]
	}
	return "multi"
}

func serveAppServerUnixSocket(ctx context.Context, flags appServerFlags) error {
	if runtime.GOOS == "windows" {
		return errors.New("unix socket app-server transport is not supported on windows")
	}
	socketPath, err := filepath.Abs(flags.socketPath)
	if err != nil {
		return fmt.Errorf("resolve socket path: %w", err)
	}
	if err := prepareAppServerSocketPath(socketPath); err != nil {
		return err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on app-server socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	connErr := make(chan error, 1)
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case stoppedErr := <-connErr:
				if errors.Is(stoppedErr, errAppServerDaemonStopped) {
					return nil
				}
				return stoppedErr
			default:
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept app-server socket connection: %w", err)
		}
		go func() {
			defer conn.Close()
			server, cleanup, err := newCLIAppServerWithTransport(flags, "socket")
			if err != nil {
				sendAppServerConnError(connErr, err)
				return
			}
			defer cleanup()
			err = appserver.ServeJSONLines(ctx, server, conn, conn)
			if server.DaemonShutdownRequested() {
				cancel()
				sendAppServerConnError(connErr, errAppServerDaemonStopped)
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				sendAppServerConnError(connErr, err)
			}
		}()
	}
}

func serveAppServerWebSocket(ctx context.Context, flags appServerFlags) error {
	mux := http.NewServeMux()
	httpServer := &http.Server{
		Addr:              flags.websocketAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: appServerWebSocketOriginAllowed,
	}
	connErr := make(chan error, 1)
	mux.HandleFunc(flags.websocketPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		server, cleanup, err := newCLIAppServerWithTransport(flags, "websocket")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			cleanup()
			return
		}
		defer cleanup()
		err = appserver.ServeWebSocket(r.Context(), server, conn)
		if server.DaemonShutdownRequested() {
			sendAppServerConnError(connErr, errAppServerDaemonStopped)
			go shutdownHTTPServer(httpServer)
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			sendAppServerConnError(connErr, err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", flags.websocketAddr)
	if err != nil {
		return fmt.Errorf("listen on app-server websocket address: %w", err)
	}
	go func() {
		<-ctx.Done()
		shutdownHTTPServer(httpServer)
	}()
	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	select {
	case err := <-connErr:
		if errors.Is(err, errAppServerDaemonStopped) {
			return nil
		}
		return err
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func prepareAppServerSocketPath(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path exists and is not a socket: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

func appServerWebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func shutdownHTTPServer(server *http.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func sendAppServerConnError(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func appServerRuntimeModelFactory(flags appServerFlags) appserver.RuntimeModelFactory {
	return func(_ context.Context, selection appserver.RuntimeModelSelection) (core.Model, appserver.RuntimeModelInfo, error) {
		provider := firstNonEmptyAppServer(selection.ProviderID, selection.Provider, flags.provider)
		if provider == "" {
			provider = detectProvider()
		}
		if provider == "" {
			return nil, appserver.RuntimeModelInfo{}, fmt.Errorf("%w: provider is required for thread/turn runtime", appserver.ErrRuntimeNotConfigured)
		}
		modelName := firstNonEmptyAppServer(selection.Model, flags.modelName)
		base, err := createModel(provider, modelName, flags.location, flags.project, deriveRequestTimeout(0))
		if err != nil {
			return nil, appserver.RuntimeModelInfo{}, err
		}
		if modelName == "" {
			modelName = strings.TrimSpace(base.ModelName())
		}
		return modelutil.NewRetryModel(base, buildRetryConfig(provider, modelName, 0)), appserver.RuntimeModelInfo{
			ProviderID: provider,
			Provider:   provider,
			Model:      modelName,
		}, nil
	}
}

func resolveAppServerStorePath(workDir, configured string) (string, error) {
	if configured == "" {
		configured = filepath.Join(workDir, defaultAppServerStoreRel)
	}
	if configured == ":memory:" {
		return configured, nil
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve store path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create store directory: %w", err)
	}
	return abs, nil
}

func firstNonEmptyAppServer(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func printAppServerUsage() {
	fmt.Fprintf(os.Stderr, `gollem app-server - Start the Codex-style JSON-RPC app-server daemon

Usage:
  gollem app-server [options]

Options:
  --stdio[=bool]            Serve newline-delimited JSON-RPC over stdio (default: true)
  --socket <path>           Serve newline-delimited JSON-RPC over a Unix domain socket
  --websocket <addr>        Serve JSON-RPC text messages over WebSocket, e.g. 127.0.0.1:0
  --websocket-path <path>   WebSocket upgrade path (default: /app-server)
  --workdir <path>          Workspace root for fs/process tools (default: current directory)
  --store <path>            SQLite store path (default: <workdir>/.gollem/appserver.db)
  --git-root <path>         Git repository root (default: workdir; unavailable if not a repo)
  --worktree-root <path>    Directory where git/worktree/create may create worktrees
  --provider <name>         Default provider for thread/turn runtime (auto-detected when possible)
  --model <name>            Default model for thread/turn runtime (provider default when unset)
  --location <region>       GCP region for vertexai providers
  --project <id>            GCP project ID for vertexai providers
  --allow-mutations[=bool]  Bypass app-server approvals for fs/process/git mutations (default: false)
  -h, --help                Show this help

Protocol:
  One JSON object per line on stdin. Responses are written as one JSON object per line on stdout.
  Unix socket transport uses the same JSONL framing. WebSocket transport uses one JSON object per text message.
`)
}
