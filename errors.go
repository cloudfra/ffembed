// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ffembed

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
)

// Errors returned by this package wrap one of these, so the kind of failure
// can be told with errors.Is. Failures of ffmpeg and ffprobe themselves are
// also a *RunError, which holds their details.
var (
	// ErrUnavailable is returned by New when ffmpeg is not installed and the
	// arguments do not permit any way of installing it that is available.
	ErrUnavailable = errors.New("ffmpeg is not available")
	// ErrInvalidRequest is returned when a request cannot be turned into
	// arguments (e.g. it has no input, or sets both raw args and fields).
	ErrInvalidRequest = errors.New("invalid request")
	// ErrFailedToStart is returned when ffmpeg or ffprobe could not be
	// started, so it never ran. A common cause is a missing dynamic loader
	// or library, RunError.Hint then holds the steps to fix it.
	ErrFailedToStart = errors.New("failed to start")
	// ErrRunFailed is returned when ffmpeg or ffprobe started and then
	// exited with an error or was killed.
	ErrRunFailed = errors.New("run failed")
	// ErrCancelled is returned by FFMpeg.Wait when ffmpeg was stopped by
	// FFMpeg.Cancel, so that it can be told from ffmpeg failing by itself.
	ErrCancelled = errors.New("run cancelled")
)

// RunError describes a failed run of ffmpeg or ffprobe.
type RunError struct {
	// Name is "ffmpeg" or "ffprobe".
	Name string
	// Binary is the path of the executable that was run.
	Binary string
	// ExitCode is the exit code of the process, -1 when it was killed or
	// never started.
	ExitCode int
	// Log holds the last lines the process wrote to stderr.
	Log string
	// Err is the error reported by os/exec.
	Err error
	// Hint describes how to fix the failure when its cause is recognized,
	// empty otherwise.
	Hint string

	// kind is the sentinel error the failure is: ErrFailedToStart,
	// ErrRunFailed, or ErrCancelled.
	kind error
}

// Error implements the error interface.
func (e *RunError) Error() string {
	s := fmt.Sprintf("%s %s, %s", e.Name, e.kind, e.Err)
	if e.Log != "" {
		s += "\n" + e.Log
	}
	if e.Hint != "" {
		s += "\nto fix: " + e.Hint
	}
	return s
}

// Unwrap returns the error reported by os/exec (e.g. an *exec.ExitError).
func (e *RunError) Unwrap() error {
	return e.Err
}

// Is reports whether target is the sentinel error for this kind of failure.
func (e *RunError) Is(target error) bool {
	return target == e.kind
}

// newRunError describes the failure err of running binary. A process that
// did not get to run is ErrFailedToStart, one stopped by Cancel is
// ErrCancelled, and any other is ErrRunFailed.
func newRunError(name string, binary string, err error, log string, cancelled bool) *RunError {
	e := &RunError{Name: name, Binary: binary, ExitCode: -1, Log: log, Err: err, kind: ErrFailedToStart}

	var exitErr *exec.ExitError
	exited := errors.As(err, &exitErr)
	if exited {
		e.ExitCode = exitErr.ExitCode()
	}
	switch {
	case cancelled:
		e.kind = ErrCancelled
	case !exited:
		e.Hint = missingLoaderHint(runtime.GOOS, binary, err)
	default:
		// With a hint the loader ended the process before ffmpeg ran.
		if e.Hint = missingLibraryHint(runtime.GOOS, e.ExitCode); e.Hint == "" {
			e.kind = ErrRunFailed
		}
	}
	return e
}

// statusDLLNotFound is the exit code of a Windows process that imports a DLL
// the loader cannot find (STATUS_DLL_NOT_FOUND, 0xC0000135).
const statusDLLNotFound = -1073741515

// missingLibraryHint returns the steps to fix a process on goos that exited
// with code before it could run, empty when code does not tell that.
func missingLibraryHint(goos string, code int) string {
	if goos != "windows" || code != statusDLLNotFound {
		return ""
	}
	return "Windows could not load a DLL the binary imports (STATUS_DLL_NOT_FOUND). " +
		"Builds of ffmpeg need avicap32.dll and msvfw32.dll (Video for Windows), which Windows Server Core " +
		"and its containers do not ship. Either (1) from an elevated PowerShell run " +
		"`Add-WindowsCapability -Online -Name ServerCore.AppCompatibility~~~~0.0.1.0` and reboot, " +
		"(2) copy both DLLs from C:\\Windows\\System32 of a Desktop Experience install of the same Windows build, " +
		"or (3) use an ffmpeg built without vfwcap by setting remote_url or prefer_installed."
}

// missingLoaderHint returns the steps to fix a start of binary on goos that
// failed with err because its dynamic loader is absent, empty when that is
// not what happened. Linux reports this as a missing file for a file that
// exists: what is missing is the ELF interpreter the binary names.
func missingLoaderHint(goos string, binary string, err error) string {
	if goos != "linux" || !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if info, statErr := os.Stat(binary); statErr != nil || !info.Mode().IsRegular() {
		return ""
	}
	return "the binary exists but the system has no dynamic loader for it. It is linked against glibc, " +
		"which musl based and libc free systems (Alpine, distroless/static, scratch images) do not provide. " +
		"Either (1) run on a glibc based system or image (e.g. debian, distroless/base), " +
		"(2) use a fully static ffmpeg by setting remote_url, or (3) install ffmpeg with the package manager " +
		"of the system (e.g. `apk add ffmpeg`) and set prefer_installed."
}
