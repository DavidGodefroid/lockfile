package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// drainGrace bounds how long Run waits for the output pipes to close once
// the plugin's process group is dead.
const drainGrace = 2 * time.Second

// Run executes s.Binary on s.Input in the sandbox. The returned error is a
// console-side failure (the sandbox could not be set up, or ctx was
// cancelled); everything the plugin does, including dying, is in the
// Outcome.
func Run(ctx context.Context, s Spec) (pluginspec.Outcome, Report, error) {
	var rep Report
	if s.Binary == nil || s.Input == nil {
		return pluginspec.Outcome{}, rep, errors.New("sandbox: spec needs a binary and an input")
	}
	l := withDefaults(s.Limits)

	in, err := reopen(s.Input)
	if err != nil {
		return pluginspec.Outcome{}, rep, fmt.Errorf("sandbox: input: %w", err)
	}
	defer in.Close()
	bin, err := reopen(s.Binary)
	if err != nil {
		return pluginspec.Outcome{}, rep, fmt.Errorf("sandbox: binary: %w", err)
	}
	defer bin.Close()

	var closers []*os.File
	defer func() {
		for _, f := range closers {
			f.Close()
		}
	}()
	pipe := func() (r, w *os.File, err error) {
		r, w, err = os.Pipe()
		if err == nil {
			closers = append(closers, r, w)
		}
		return
	}
	outR, outW, err := pipe()
	if err != nil {
		return pluginspec.Outcome{}, rep, err
	}
	errR, errW, err := pipe()
	if err != nil {
		return pluginspec.Outcome{}, rep, err
	}
	statR, statW, err := pipe()
	if err != nil {
		return pluginspec.Outcome{}, rep, err
	}

	cmd := &exec.Cmd{
		Path: "/proc/self/exe",
		Args: []string{"lockfile-sandbox", trampolineArg,
			strconv.Itoa(l.CPUSeconds), strconv.FormatInt(l.Memory, 10)},
		Env:    []string{protocolEnv},
		Dir:    "/",
		Stdout: outW,
		Stderr: errW,
		// fd 3 input, fd 4 plugin binary, fd 5 trampoline status.
		ExtraFiles:  []*os.File{in, bin, statW},
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL},
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return pluginspec.Outcome{}, rep, fmt.Errorf("sandbox: start: %w", err)
	}
	pid := cmd.Process.Pid
	rep.PID = pid
	outW.Close()
	errW.Close()
	statW.Close()

	type stdout struct {
		b         []byte
		truncated bool
	}
	outc := make(chan stdout, 1)
	go func() {
		b, tr := readCapped(outR, stdoutCap)
		outc <- stdout{b, tr}
	}()
	errc := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(io.Discard, errR)
		errc <- n
	}()
	statc := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(statR, 4096))
		statc <- string(b)
	}()

	// Wait for the exit without reaping, so the process group id stays
	// reserved until the whole group has been killed.
	exited := make(chan struct{})
	go func() {
		var info unix.Siginfo
		for {
			err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if err != unix.EINTR {
				break
			}
		}
		close(exited)
	}()
	timer := time.NewTimer(l.Timeout)
	defer timer.Stop()
	var timedOut bool
	var ctxErr error
	select {
	case <-exited:
	case <-timer.C:
		timedOut = true
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	case <-ctx.Done():
		ctxErr = ctx.Err()
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	}
	// Whatever the plugin left in its group goes too.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Wait()
	rep.Duration = time.Since(start)

	// One deadline for both pipes: a closed channel, readable by each select
	// (a timer's channel fires once).
	grace := make(chan struct{})
	t := time.AfterFunc(drainGrace, func() { close(grace) })
	defer t.Stop()
	var out stdout
	select {
	case out = <-outc:
	case <-grace:
		outR.Close()
		out = <-outc
	}
	select {
	case rep.StderrBytes = <-errc:
	case <-grace:
		errR.Close()
		rep.StderrBytes = <-errc
	}
	status := <-statc

	ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	rep.ExitCode = ws.ExitStatus()
	if ws.Signaled() {
		rep.Signal = unix.SignalName(ws.Signal())
	}

	switch {
	case ctxErr != nil:
		return pluginspec.Failed(pluginspec.CauseTimeout), rep, ctxErr
	case status != "":
		if strings.HasPrefix(status, ErrUnsupported.Error()) {
			return pluginspec.Outcome{}, rep, fmt.Errorf("%w: %s", ErrUnsupported, status)
		}
		return pluginspec.Outcome{}, rep, fmt.Errorf("sandbox: setup: %s", status)
	case timedOut:
		return pluginspec.Failed(pluginspec.CauseTimeout), rep, nil
	case ws.Signaled() && ws.Signal() == syscall.SIGSYS:
		return pluginspec.Failed(pluginspec.CauseSandbox), rep, nil
	}
	return pluginspec.ParseAnswer(out.b, out.truncated, ws.ExitStatus(), ws.Signaled()), rep, nil
}

// reopen gives a fresh, read-only open file description of f: its own
// offset, no write access whatever f was opened with.
func reopen(f *os.File) (*os.File, error) {
	return os.OpenFile(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), os.O_RDONLY|syscall.O_CLOEXEC, 0)
}

// readCapped keeps the first max bytes of r and drains the rest, so a
// plugin writing without end neither blocks nor dies of SIGPIPE before its
// exit status is known. truncated reports that max bytes or more arrived.
func readCapped(r io.Reader, max int) ([]byte, bool) {
	buf := make([]byte, 0, max)
	tmp := make([]byte, 4096)
	truncated := false
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if room := max - len(buf); room > 0 {
				buf = append(buf, tmp[:min(n, room)]...)
			}
			if len(buf) >= max {
				truncated = true
			}
		}
		if err != nil {
			return buf, truncated
		}
	}
}
