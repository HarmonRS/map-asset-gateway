//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName        = "BasemapPusher"
	serviceDisplayName = "Basemap Pusher"
	serviceDescription = "Internal catalog and push gateway for existing basemap tiles and vector assets."
	// appEnvFileFlag is baked into the service command line so the service finds
	// its config file even though the SCM starts it in C:\Windows\System32.
	appEnvFileFlag = "--env-file"
)

// handleServiceArgs implements the install/uninstall/start/stop/status verbs.
// The bool reports whether the verb was recognised.
func handleServiceArgs(command, envFile string) (bool, error) {
	switch command {
	case "install":
		return true, installService(envFile)
	case "uninstall":
		return true, uninstallService()
	case "start":
		return true, startService()
	case "stop":
		return true, stopService()
	case "status":
		return true, serviceStatus()
	default:
		return false, nil
	}
}

// runAsWindowsService reports whether the process was launched by the SCM, and
// if so runs it to completion. It returns false for a normal console launch.
func runAsWindowsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	restoreLog := setupServiceLogging()
	defer restoreLog()

	if err := svc.Run(serviceName, &serviceHandler{}); err != nil {
		log.Printf("windows service run: %v", err)
	}
	return true
}

type serviceHandler struct{}

func (h *serviceHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("service stopped with error: %v", err)
				changes <- svc.Status{State: svc.Stopped}
				return false, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case change := <-r:
			switch change.Cmd {
			case svc.Interrogate:
				changes <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				// Give the HTTP server its drain window before reporting stopped.
				select {
				case <-done:
				case <-time.After(shutdownGrace + 5*time.Second):
					log.Printf("service did not stop within the drain window")
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

func installService(envFile string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	resolvedEnv := envFile
	if resolvedEnv == "" {
		resolvedEnv = findDefaultEnvFile(filepath.Dir(exe))
	}
	if resolvedEnv == "" {
		return fmt.Errorf("no config/service.env found next to %s; pass --env-file <path> to install", exe)
	}
	resolvedEnv, err = filepath.Abs(resolvedEnv)
	if err != nil {
		return fmt.Errorf("resolve env file: %w", err)
	}
	if _, err := os.Stat(resolvedEnv); err != nil {
		return fmt.Errorf("env file %s is not readable: %w", resolvedEnv, err)
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run this as Administrator): %w", err)
	}
	defer manager.Disconnect()

	if existing, err := manager.OpenService(serviceName); err == nil {
		existing.Close()
		return fmt.Errorf("service %q already exists; run uninstall first", serviceName)
	}

	service, err := manager.CreateService(serviceName, exe, mgr.Config{
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		StartType:   mgr.StartAutomatic,
	}, appEnvFileFlag, resolvedEnv)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer service.Close()

	// Restart on failure: 5s, 15s, 60s, then keep trying within the reset window.
	if err := service.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400); err != nil {
		log.Printf("warning: could not set recovery actions: %v", err)
	}

	log.Printf("installed service %q", serviceName)
	log.Printf("  executable: %s", exe)
	log.Printf("  env file:   %s", resolvedEnv)
	log.Printf("  start with: %s start", exe)
	return nil
}

func uninstallService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run this as Administrator): %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer service.Close()

	if status, err := service.Query(); err == nil && status.State != svc.Stopped {
		_, _ = service.Control(svc.Stop)
		time.Sleep(2 * time.Second)
	}
	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	log.Printf("uninstalled service %q", serviceName)
	return nil
}

func startService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer service.Close()

	if err := service.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	log.Printf("started service %q", serviceName)
	return nil
}

func stopService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer service.Close()

	status, err := service.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for status.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		status, err = service.Query()
		if err != nil {
			return fmt.Errorf("query service: %w", err)
		}
	}
	log.Printf("stopped service %q", serviceName)
	return nil
}

func serviceStatus() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}
	names := map[svc.State]string{
		svc.Stopped:         "stopped",
		svc.StartPending:    "start pending",
		svc.StopPending:     "stop pending",
		svc.Running:         "running",
		svc.ContinuePending: "continue pending",
		svc.PausePending:    "pause pending",
		svc.Paused:          "paused",
	}
	name, ok := names[status.State]
	if !ok {
		name = fmt.Sprintf("state %d", status.State)
	}
	fmt.Printf("%s: %s\n", serviceName, name)
	return nil
}

// findDefaultEnvFile looks in the usual layouts: next to the executable, then
// one and two levels up (services/api-go -> repo root).
func findDefaultEnvFile(startDir string) string {
	candidates := []string{
		filepath.Join(startDir, "config", "service.env"),
		filepath.Join(startDir, "..", "config", "service.env"),
		filepath.Join(startDir, "..", "..", "config", "service.env"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// setupServiceLogging points the standard logger at a file, because a Windows
// service has no console and would otherwise discard every log line. It returns
// a restore func that is safe to call even when nothing was configured.
func setupServiceLogging() func() {
	path := strings.TrimSpace(os.Getenv("SERVICE_LOG_FILE"))
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return func() {}
		}
		path = filepath.Join(filepath.Dir(exe), "logs", "basemap-pusher.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	writer, err := newRotatingFileWriter(path, serviceLogMaxBytes)
	if err != nil {
		return func() {}
	}
	log.SetOutput(writer)
	log.Printf("service log: %s (rotates at %d MB)", path, serviceLogMaxBytes>>20)
	return func() { _ = writer.Close() }
}

const serviceLogMaxBytes = 10 << 20

// rotatingFileWriter appends to one file and rolls it to ".1" once it grows
// past maxBytes, so a long-running service cannot fill the disk.
type rotatingFileWriter struct {
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func newRotatingFileWriter(path string, maxBytes int64) (*rotatingFileWriter, error) {
	writer := &rotatingFileWriter{path: path, maxBytes: maxBytes}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *rotatingFileWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes {
		w.roll()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingFileWriter) roll() {
	if w.file != nil {
		_ = w.file.Close()
	}
	// Windows refuses to rename onto an existing path, so drop the old roll.
	_ = os.Remove(w.path + ".1")
	_ = os.Rename(w.path, w.path+".1")
	w.size = 0
	if err := w.open(); err != nil {
		// Fall back to appending to the original path if the reopen failed.
		_ = err
	}
}

func (w *rotatingFileWriter) Close() error {
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}
