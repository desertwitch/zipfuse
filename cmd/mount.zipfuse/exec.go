//nolint:mnd,noctx,err113
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"al.essio.dev/pkg/shellescape"
)

var (
	errMountTimeout = errors.New("mount timeout")
	errMountFailed  = errors.New("mount failed")
)

func (mh *mountHelper) BuildCommand() []string {
	var parts []string

	if mh.Binary == "" {
		parts = append(parts, mh.Type)
	} else {
		parts = append(parts, mh.Binary)
	}

	parts = append(parts, mh.Source)
	parts = append(parts, mh.Mountpoint)
	parts = append(parts, mh.BuildOptions()...)

	return parts
}

func (mh *mountHelper) BuildOptions() []string {
	var parts []string

	if len(mh.Options) > 0 {
		keys := make([]string, 0, len(mh.Options))
		for k := range mh.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			val := mh.Options[key]
			if val == "" {
				parts = append(parts, "--"+key)
			} else {
				parts = append(parts, "--"+key)
				parts = append(parts, val)
			}
		}
	}

	return parts
}

func (mh *mountHelper) Execute() error {
	// We must always set up our "parent" environment first,
	// because [exec.Command] internally requires a sane $PATH.
	mh.setupEnvironment()

	cmdArgs := mh.BuildCommand()
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = os.Environ()

	spa := &syscall.SysProcAttr{Setsid: true}
	if mh.Setuid != "" {
		cmd, spa = mh.setUID(spa, cmd, cmdArgs)
	}
	cmd.SysProcAttr = spa

	fdnull, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open \"/dev/null\": %w", err)
	}
	defer fdnull.Close()

	fdlog, err := os.OpenFile(mh.Logfile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		fmt.Fprintf(os.Stderr, `mount.zipfuse warning: failed to open %q: %v (falling back to "/dev/null").
Do try to pass "xlog=/full/path/to/writeable/logfile" as a mount option.
`, mh.Logfile, err)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = fdnull, fdnull, fdnull
	} else {
		defer fdlog.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = fdnull, fdlog, fdlog
	}

	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("pipe error: %w", err)
	}
	defer r.Close()
	cmd.Env = append(cmd.Env, "ZIPFUSE_HELPER_FD=3")
	cmd.ExtraFiles = []*os.File{w}

	if err := cmd.Start(); err != nil {
		w.Close()

		return fmt.Errorf("process error: %w", err)
	}
	_ = cmd.Process.Release()
	w.Close()

	if err := mh.waitForMount(r); err != nil {
		return fmt.Errorf("mount error: %w", err)
	}

	return nil
}

func (mh *mountHelper) setupEnvironment() {
	currentPath := os.Getenv("PATH")
	additionalPath := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if currentPath == "" {
		os.Setenv("PATH", additionalPath)
	} else {
		os.Setenv("PATH", currentPath+":"+additionalPath)
	}

	if mh.Setuid == "" && os.Getenv("HOME") == "" {
		os.Setenv("HOME", "/root")
	}
}

func (mh *mountHelper) setUID(spa *syscall.SysProcAttr, cmd *exec.Cmd, cmdArgs []string) (*exec.Cmd, *syscall.SysProcAttr) {
	home, uid, gid, err := resolveUser(mh.Setuid)
	if err == nil {
		if home != "" {
			cmd.Env = append(cmd.Env, "HOME="+home)
		}
		spa.Credential = &syscall.Credential{
			Uid: uid,
			Gid: gid,
		}
	} else {
		fmt.Fprintf(os.Stderr, "mount.zipfuse warning: failed to resolve user %q: %v (falling back to \"su\")\n",
			mh.Setuid, err)

		safeCmdArgs := make([]string, len(cmdArgs))
		for i, arg := range cmdArgs {
			safeCmdArgs[i] = shellescape.Quote(arg)
		}
		innerCmdLine := strings.Join(safeCmdArgs, " ")
		outerCmdLine := fmt.Sprintf("su - %s -c %s", shellescape.Quote(mh.Setuid), shellescape.Quote(innerCmdLine))

		restoreEnv := make([]string, len(cmd.Env))
		copy(restoreEnv, cmd.Env)

		cmd = exec.Command("/bin/sh", "-c", outerCmdLine)
		cmd.Env = restoreEnv
	}

	return cmd, spa
}

func (mh *mountHelper) waitForMount(r io.Reader) error {
	signalDone := mh.waitForSignal(r)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	totalTimeout := time.After(mh.Timeout)
	for {
		select {
		case signalErr := <-signalDone:
			if signalErr == nil {
				return nil
			} else if errors.Is(signalErr, errMountFailed) {
				return signalErr
			}
			fmt.Fprintf(os.Stderr, "mount.zipfuse warning: %v\n", signalErr)
			signalDone = nil

		case <-ticker.C:
			if isMounted, _ := mh.checkMountTable(); isMounted {
				return nil
			}

		case <-totalTimeout:
			if isMounted, _ := mh.checkMountTable(); isMounted {
				return nil
			}

			return errMountTimeout
		}
	}
}

func (mh *mountHelper) waitForSignal(r io.Reader) <-chan error {
	signalChan := make(chan error, 1)

	go func() {
		defer func() {
			rec := recover()
			if rec != nil {
				select {
				case signalChan <- fmt.Errorf("panic recovered: %v", rec):
				default:
				}
			}
			close(signalChan)
		}()

		status := make([]byte, 1)
		_, err := r.Read(status)
		if err != nil {
			signalChan <- fmt.Errorf("failed to read from pipe: %w", err)

			return
		}

		if status[0] == 0 {
			signalChan <- nil
		} else {
			scanner := bufio.NewScanner(r)
			if scanner.Scan() {
				signalChan <- fmt.Errorf("%w: %s", errMountFailed, scanner.Text())
			} else if err := scanner.Err(); err != nil {
				signalChan <- fmt.Errorf("failed to read from pipe: %w", err)
			} else {
				signalChan <- errors.New("failed to parse message from pipe")
			}
		}
	}()

	return signalChan
}

func (mh *mountHelper) checkMountTable() (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("cannot open \"/proc/self/mountinfo\": %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, " "+mh.Mountpoint+" ") {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("error reading \"/proc/self/mountinfo\": %w", err)
	}

	return false, nil
}
