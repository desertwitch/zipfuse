//nolint:mnd,err113,noctx
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

func (mh *MountHelper) BuildCommand() []string {
	var parts []string

	parts = append(parts, mh.Type)
	parts = append(parts, mh.Source)
	parts = append(parts, mh.Mountpoint)
	parts = append(parts, mh.BuildOptions()...)

	return parts
}

func (mh *MountHelper) BuildOptions() []string {
	parts := []string{}

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

func (mh *MountHelper) Execute() error {
	mh.setupEnvironment()

	cmdArgs := mh.BuildCommand()
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)

	spa := &syscall.SysProcAttr{Setsid: true}
	if mh.Setuid != "" {
		uid, gid, err := resolveUser(mh.Setuid)
		if err == nil {
			spa.Credential = &syscall.Credential{
				Uid: uid,
				Gid: gid,
			}
		} else {
			safeCmdArgs := make([]string, len(cmdArgs))
			for i, arg := range cmdArgs {
				safeCmdArgs[i] = shellescape.Quote(arg)
			}
			innerCmdLine := strings.Join(safeCmdArgs, " ")
			outerCmdLine := fmt.Sprintf("su - %s -c %s", shellescape.Quote(mh.Setuid), shellescape.Quote(innerCmdLine))
			cmd = exec.Command("/bin/sh", "-c", outerCmdLine)
		}
	}
	cmd.SysProcAttr = spa

	fd, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/null: %w", err)
	}
	defer fd.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = fd, fd, fd

	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("pipe error: %w", err)
	}
	defer r.Close()
	cmd.Env = append(os.Environ(), "ZIPFUSE_HELPER_FD=3")
	cmd.ExtraFiles = []*os.File{w}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("process error: %w", err)
	}
	_ = cmd.Process.Release()
	w.Close()

	if err := mh.waitForMount(r); err != nil {
		return fmt.Errorf("mount error: %w", err)
	}

	return nil
}

func (mh *MountHelper) setupEnvironment() {
	if mh.Setuid == "" && os.Getenv("HOME") == "" {
		os.Setenv("HOME", "/root")
	}

	currentPath := os.Getenv("PATH")
	additionalPath := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if currentPath == "" {
		os.Setenv("PATH", additionalPath)
	} else {
		os.Setenv("PATH", currentPath+":"+additionalPath)
	}
}

func (mh *MountHelper) waitForMount(r io.Reader) error {
	signalDone := make(chan error, 1)
	go func() {
		defer close(signalDone)
		buf := make([]byte, 1)
		_, err := r.Read(buf)
		if err == nil {
			signalDone <- nil
		} else {
			signalDone <- err
		}
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	totalTimeout := time.After(mountTimeout)
	for {
		select {
		case signalErr := <-signalDone:
			if signalErr == nil {
				return nil
			}
			signalDone = nil

		case <-ticker.C:
			if isMounted, _ := mh.checkMountTable(); isMounted {
				return nil
			}

		case <-totalTimeout:
			if isMounted, _ := mh.checkMountTable(); isMounted {
				return nil
			}

			return errors.New("timed out: mountpoint not found")
		}
	}
}

func (mh *MountHelper) checkMountTable() (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("cannot open /proc/self/mountinfo: %w", err)
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
		return false, fmt.Errorf("error reading /proc/self/mountinfo: %w", err)
	}

	return false, nil
}
