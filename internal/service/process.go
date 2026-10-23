package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	slogx "github.com/KLORQ/quarshell-core/slogx"
	"github.com/KLORQ/quarshell-driver/driver"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"golang.org/x/term"
)

// ExecOpts for quarshell sandbox exec.
type ExecOpts struct {
	Name              string
	Argv              []string
	TTY               bool
	Env               []string
	WorkDir           string
	Timeout           time.Duration // zero leaves execution bounded only by the caller context
	NoLoginShell      bool
	AttachMainProcess bool
	Stdin             io.Reader
}

// Exec runs a command in a sandbox.

// Exec runs a command in a sandbox.
func (a *App) Exec(opt ExecOpts) error {
	if len(opt.Argv) == 0 {
		return fmt.Errorf("usage: quarshell sandbox exec [name] [--workdir DIR] [--env K=V] -- CMD")
	}
	name, err := a.resolveSandboxTarget(opt.Name)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	opt.Name = name
	const op = "cli.sandbox.exec"
	log := a.op(op, slog.String("sandbox", opt.Name), slog.Int("argv_len", len(opt.Argv)))
	if opt.Name == "" || len(opt.Argv) == 0 {
		return fmt.Errorf("usage: quarshell sandbox exec [name] [--workdir DIR] [--env K=V] -- CMD")
	}
	log.Info("executing in sandbox", slog.Bool("tty", opt.TTY), slog.Bool("no_login_shell", opt.NoLoginShell))
	commandCtx := a.CommandContext()
	routeCtx, routeCancel := context.WithTimeout(commandCtx, TimeoutAPI)
	client, err := a.lifecycleGatewaySandbox(routeCtx, opt.Name)
	routeCancel()
	if err != nil {
		log.Error("failed to resolve sandbox runtime", slogx.Err(err))
		return fmt.Errorf("exec: %w", err)
	}
	defer client.Close()
	execCtx := commandCtx
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(commandCtx, opt.Timeout+TimeoutExecGrace)
		defer cancel()
	}
	return a.execGateway(execCtx, client, opt, log)
}

func writeExecResult(result driver.ExecResult) error {
	if len(result.Stdout) > 0 {
		if _, err := os.Stdout.Write(result.Stdout); err != nil {
			return fmt.Errorf("write command stdout: %w", err)
		}
	}
	if len(result.Stderr) > 0 {
		if _, err := os.Stderr.Write(result.Stderr); err != nil {
			return fmt.Errorf("write command stderr: %w", err)
		}
	}
	if result.OutputTruncated {
		if _, err := fmt.Fprintln(os.Stderr, "quarshell: command output was truncated by the compute driver"); err != nil {
			return fmt.Errorf("write command truncation notice: %w", err)
		}
	}
	return nil
}

func (a *App) execGateway(ctx context.Context, client *klorq.Client, opt ExecOpts, log *slogx.Logger) error {
	options := klorq.SandboxExecOptions{WorkDir: opt.WorkDir, NoLoginShell: opt.NoLoginShell, Env: execEnvMap(opt.Env), AttachMainProcess: opt.AttachMainProcess}
	a.emitProc(opt.Name, "LAUNCH", strings.Join(opt.Argv, " "), 0)
	var code int
	var err error
	if opt.TTY || opt.Stdin != nil {
		code, err = runGatewayInteractive(ctx, client, a.workspaceName(), opt, options)
	} else {
		code, err = runGatewayStream(ctx, client, a.workspaceName(), opt, options)
	}
	if err == nil {
		a.rememberLastSandbox(opt.Name, "")
	}
	a.emitProc(opt.Name, "EXIT", strings.Join(opt.Argv, " "), code)
	if err != nil {
		log.Error("gateway exec failed", slogx.Err(err), slog.Int("exit_code", code))
		return err
	}
	if code != 0 {
		log.Info("gateway exec finished with non-zero exit", slog.Int("exit_code", code))
		return &ExitError{Code: code}
	}
	log.Info("gateway exec finished", slog.Int("exit_code", code))
	return nil
}

func execEnvMap(entries []string) map[string]string {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

type gatewayExecClient interface {
	ExecStreamingWithTimeout(context.Context, string, string, []string, time.Duration, klorq.SandboxExecOptions) (klorq.SandboxExecStream, error)
	ExecInteractiveWithTTYTimeout(context.Context, string, string, []string, bool, uint32, uint32, time.Duration, klorq.SandboxExecOptions) (klorq.SandboxInteractiveOutput, error)
}

type resizableExecSession interface {
	Resize(cols, rows uint32) error
}

func runGatewayStream(ctx context.Context, client gatewayExecClient, workspace string, opt ExecOpts, options klorq.SandboxExecOptions) (int, error) {
	stream, err := client.ExecStreamingWithTimeout(ctx, workspace, opt.Name, opt.Argv, opt.Timeout, options)
	if err != nil {
		return -1, err
	}
	defer stream.Close()
	for {
		chunk, nextErr := stream.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return -1, nextErr
		}
		writer := io.Writer(os.Stdout)
		if chunk.Stream == "stderr" {
			writer = os.Stderr
		}
		if _, err := writer.Write(chunk.Data); err != nil {
			return -1, fmt.Errorf("write sandbox output: %w", err)
		}
	}
	return stream.ExitCode()
}

func runGatewayInteractive(ctx context.Context, client gatewayExecClient, workspace string, opt ExecOpts, options klorq.SandboxExecOptions) (int, error) {
	var cols, rows uint32
	if opt.TTY {
		if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			cols, rows = uint32(width), uint32(height)
		}
	}
	session, err := client.ExecInteractiveWithTTYTimeout(ctx, workspace, opt.Name, opt.Argv, opt.TTY, cols, rows, opt.Timeout, options)
	if err != nil {
		return -1, err
	}
	defer session.Close()
	var restore func() error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		state, rawErr := term.MakeRaw(int(os.Stdin.Fd()))
		if rawErr != nil {
			return -1, fmt.Errorf("enable terminal raw mode: %w", rawErr)
		}
		restore = func() error { return term.Restore(int(os.Stdin.Fd()), state) }
		defer restore()
	}
	if opt.TTY {
		stopResize := watchTerminalResize(ctx, session)
		defer stopResize()
	}
	inputDone := make(chan error, 1)
	go func() {
		if opt.Stdin == nil {
			inputDone <- klorq.CloseSandboxExecInput(session)
			return
		}
		_, copyErr := io.Copy(session, opt.Stdin)
		closeErr := klorq.CloseSandboxExecInput(session)
		inputDone <- errors.Join(copyErr, closeErr)
	}()
	outputErr := copyGatewayInteractiveOutput(session)
	code, exitErr := session.ExitCode()
	var inputErr error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		select {
		case inputErr = <-inputDone:
		default:
			// A terminal reader cannot be interrupted portably after the remote command exits.
		}
	} else {
		inputErr = <-inputDone
	}
	return code, errors.Join(outputErr, inputErr, exitErr)
}

func copyGatewayInteractiveOutput(session klorq.SandboxInteractiveOutput) error {
	for {
		chunk, err := session.NextOutput()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		writer := io.Writer(os.Stdout)
		if chunk.Stream == "stderr" {
			writer = os.Stderr
		}
		if _, err := writer.Write(chunk.Data); err != nil {
			return fmt.Errorf("write sandbox output: %w", err)
		}
	}
}

func (a *App) emitProc(sandbox, activity, details string, exitCode int) {
	gw, err := a.currentGatewayURL()
	if err != nil || gw == "" || sandbox == "" {
		return
	}
	ts := time.Now().UTC()
	text := fmt.Sprintf("%s OCSF PROC:%s [INFO] ALLOWED %s", ts.Format(time.RFC3339Nano), activity, details)
	if activity == "EXIT" {
		text = fmt.Sprintf("%s OCSF PROC:EXIT [INFO] ALLOWED %s [exit:%d]", ts.Format(time.RFC3339Nano), details, exitCode)
	}
	c := a.clientWithToken(gw, a.gatewayTokenForURL(gw))
	ctx, cancel := a.withTimeout(TimeoutEmit)
	defer cancel()
	_ = c.PostLogs(ctx, sandbox, []klorq.LogLine{{
		TS: ts, Source: "proc", Level: "INFO", Text: text,
	}})
}

// ProxyOpts for foreground host-side CONNECT proxy (debug / host-proxy mode).
