package adapterapi

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

type RunSpec struct {
	Args              []string
	StdinContent      string
	UseStdin          bool
	LastMessagePath   string
	NativeSessionMeta map[string]any
	Cleanup           func() error
}

type Builder interface {
	StartArgs(req StartRunRequest) (RunSpec, error)
	ContinueArgs(req ContinueRunRequest) (RunSpec, error)
}

type BaseAdapter struct {
	name    string
	binary  string
	enabled bool
	caps    Capabilities
	Builder
}

func NewBaseAdapter(name, binary string, enabled bool, caps Capabilities, builder Builder) *BaseAdapter {
	return &BaseAdapter{
		name:    name,
		binary:  binary,
		enabled: enabled,
		caps:    caps,
		Builder: builder,
	}
}

func (a *BaseAdapter) Name() string               { return a.name }
func (a *BaseAdapter) Binary() string             { return a.binary }
func (a *BaseAdapter) Implemented() bool          { return true }
func (a *BaseAdapter) Capabilities() Capabilities { return a.caps }

func (a *BaseAdapter) Detect(ctx context.Context) (Diagnosis, error) {
	_, err := exec.LookPath(a.binary)
	version, versionErr := DetectVersion(ctx, a.binary, "--version")
	return Diagnosis{
		Adapter:      a.Name(),
		Binary:       a.binary,
		Version:      version,
		Available:    err == nil,
		Enabled:      a.enabled,
		Implemented:  a.Implemented(),
		Capabilities: a.Capabilities(),
	}, versionErr
}

func (a *BaseAdapter) StartRun(ctx context.Context, req StartRunRequest) (*RunHandle, error) {
	spec, err := a.StartArgs(req)
	if err != nil {
		return nil, err
	}
	return a.exec(ctx, req.CWD, spec)
}

func (a *BaseAdapter) ContinueRun(ctx context.Context, req ContinueRunRequest) (*RunHandle, error) {
	spec, err := a.ContinueArgs(req)
	if err != nil {
		return nil, err
	}
	return a.exec(ctx, req.CWD, spec)
}

func (a *BaseAdapter) exec(ctx context.Context, cwd string, spec RunSpec) (*RunHandle, error) {
	cmd := exec.CommandContext(ctx, a.binary, spec.Args...)
	cmd.Dir = cwd
	PrepareCommand(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open %s stdout: %w", a.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open %s stderr: %w", a.name, err)
	}

	var stdin io.WriteCloser
	if spec.UseStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("open %s stdin: %w", a.name, err)
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", a.name, err)
	}

	if stdin != nil {
		go func() {
			_, _ = stdin.Write([]byte(spec.StdinContent))
			_ = stdin.Close()
		}()
	}

	cleanup := spec.Cleanup
	if cleanup == nil {
		cleanup = func() error { return nil }
	}

	return &RunHandle{
		Cmd:               cmd,
		Stdout:            stdout,
		Stderr:            stderr,
		LastMessagePath:   spec.LastMessagePath,
		NativeSessionMeta: spec.NativeSessionMeta,
		Cleanup:           cleanup,
	}, nil
}
