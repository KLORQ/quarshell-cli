package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

type fakeGatewayExecClient struct {
	stream  *fakeGatewayExecStream
	session *fakeGatewayInteractiveSession
	got     []string
	ws      string
	name    string
	opt     klorq.SandboxExecOptions
	tty     bool
	cols    uint32
	rows    uint32
	timeout time.Duration
}

func (f *fakeGatewayExecClient) ExecStreamingWithTimeout(_ context.Context, workspace, name string, argv []string, timeout time.Duration, options klorq.SandboxExecOptions) (klorq.SandboxExecStream, error) {
	f.ws, f.name, f.got, f.opt, f.timeout = workspace, name, argv, options, timeout
	return f.stream, nil
}

func (f *fakeGatewayExecClient) ExecInteractiveWithTTYTimeout(_ context.Context, workspace, name string, argv []string, tty bool, cols, rows uint32, timeout time.Duration, options klorq.SandboxExecOptions) (klorq.SandboxInteractiveOutput, error) {
	f.ws, f.name, f.got, f.opt, f.timeout = workspace, name, argv, options, timeout
	f.tty, f.cols, f.rows = tty, cols, rows
	return f.session, nil
}

type fakeGatewayInteractiveSession struct {
	output *strings.Reader
	chunks []*klorq.SandboxExecChunk
	input  strings.Builder
	closed bool
}

func (s *fakeGatewayInteractiveSession) Read(data []byte) (int, error)  { return s.output.Read(data) }
func (s *fakeGatewayInteractiveSession) Write(data []byte) (int, error) { return s.input.Write(data) }
func (*fakeGatewayInteractiveSession) Resize(uint32, uint32) error      { return nil }
func (*fakeGatewayInteractiveSession) ExitCode() (int, error)           { return 7, nil }
func (*fakeGatewayInteractiveSession) Close() error                     { return nil }
func (s *fakeGatewayInteractiveSession) CloseWrite() error              { s.closed = true; return nil }
func (s *fakeGatewayInteractiveSession) NextOutput() (*klorq.SandboxExecChunk, error) {
	if len(s.chunks) > 0 {
		chunk := s.chunks[0]
		s.chunks = s.chunks[1:]
		return chunk, nil
	}
	return nil, io.EOF
}

type resizeRecorder struct{ sizes chan [2]uint32 }

func (r resizeRecorder) Resize(cols, rows uint32) error {
	r.sizes <- [2]uint32{cols, rows}
	return nil
}

func TestWatchResizeEventsForwardsCurrentDimensions(t *testing.T) {
	events := make(chan os.Signal, 1)
	session := resizeRecorder{sizes: make(chan [2]uint32, 1)}
	stop := watchResizeEvents(context.Background(), events, func() (uint32, uint32, error) {
		return 121, 44, nil
	}, session)
	events <- os.Interrupt
	select {
	case size := <-session.sizes:
		if size != [2]uint32{121, 44} {
			t.Fatalf("resize = %v, want [121 44]", size)
		}
	case <-time.After(time.Second):
		t.Fatal("resize event was not forwarded")
	}
	stop()
}

type fakeGatewayExecStream struct {
	chunks []*klorq.SandboxExecChunk
	code   int
}

func TestGatewayExecInteractiveSupportsNonTTYStdin(t *testing.T) {
	oldStdout, oldStderr, oldStdin := os.Stdout, os.Stderr, os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = stderrWriter
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = stdinReader
	t.Cleanup(func() {
		os.Stdout, os.Stderr, os.Stdin = oldStdout, oldStderr, oldStdin
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
	})
	session := &fakeGatewayInteractiveSession{output: strings.NewReader("outputwarn"), chunks: []*klorq.SandboxExecChunk{
		{Stream: "stdout", Data: []byte("output")}, {Stream: "stderr", Data: []byte("warn")},
	}}
	client := &fakeGatewayExecClient{session: session}
	code, runErr := runGatewayInteractive(context.Background(), client, "team", ExecOpts{
		Name: "shared", Argv: []string{"cat"}, Stdin: strings.NewReader("input"), TTY: false, Timeout: 5 * time.Second,
	}, klorq.SandboxExecOptions{WorkDir: "/work"})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(r)
	_ = r.Close()
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	stderrOut, stderrErr := io.ReadAll(stderrReader)
	_ = stderrReader.Close()
	if runErr != nil || readErr != nil || stderrErr != nil || code != 7 || string(out) != "output" || string(stderrOut) != "warn" {
		t.Fatalf("interactive result: code=%d err=%v stdout=%q stderr=%q readErr=%v stderrErr=%v", code, runErr, out, stderrOut, readErr, stderrErr)
	}
	if !session.closed || session.input.String() != "input" || client.tty || client.cols != 0 || client.rows != 0 || client.timeout != 5*time.Second {
		t.Fatalf("interactive input/tty/timeout mismatch: closed=%v input=%q tty=%v size=%dx%d timeout=%s", session.closed, session.input.String(), client.tty, client.cols, client.rows, client.timeout)
	}
}

func (s *fakeGatewayExecStream) Next() (*klorq.SandboxExecChunk, error) {
	if len(s.chunks) == 0 {
		return nil, io.EOF
	}
	chunk := s.chunks[0]
	s.chunks = s.chunks[1:]
	return chunk, nil
}

func (s *fakeGatewayExecStream) ExitCode() (int, error) { return s.code, nil }
func (*fakeGatewayExecStream) Close() error             { return nil }

func TestGatewayExecStreamPreservesOutputOptionsAndExitCode(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })
	client := &fakeGatewayExecClient{stream: &fakeGatewayExecStream{
		chunks: []*klorq.SandboxExecChunk{{Stream: "stdout", Data: []byte("hello\n")}}, code: 19,
	}}
	code, runErr := runGatewayStream(context.Background(), client, "team", ExecOpts{Name: "shared", Argv: []string{"printf", "hello"}, Timeout: 4 * time.Second}, klorq.SandboxExecOptions{
		WorkDir: "/work", Env: map[string]string{"MODE": "test"}, NoLoginShell: true,
	})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil || readErr != nil || code != 19 || string(out) != "hello\n" {
		t.Fatalf("stream result: code=%d err=%v output=%q readErr=%v", code, runErr, out, readErr)
	}
	if client.ws != "team" || client.name != "shared" || strings.Join(client.got, " ") != "printf hello" || client.opt.WorkDir != "/work" || client.opt.Env["MODE"] != "test" || !client.opt.NoLoginShell || client.timeout != 4*time.Second {
		t.Fatalf("stream request not preserved: %+v", client)
	}
}

func TestGatewayLogSourceTreatsAllAsUnfiltered(t *testing.T) {
	if got := gatewayLogSource("all"); got != "" {
		t.Fatalf("gateway all-source filter=%q; want empty filter", got)
	}
	if got := gatewayLogSource("proxy"); got != "proxy" {
		t.Fatalf("gateway proxy-source filter=%q", got)
	}
}

func TestExitCodeFindsWrappedRemoteExitError(t *testing.T) {
	err := fmt.Errorf("sandbox command: %w", &ExitError{Code: 23})
	if code, ok := ExitCode(err); !ok || code != 23 {
		t.Fatalf("ExitCode(%v)=(%d,%v); want (23,true)", err, code, ok)
	}
}
