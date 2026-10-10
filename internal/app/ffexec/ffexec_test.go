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

package ffexec

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cloudfra/ffembed"
	"github.com/cloudfra/ffembed/internal/common"
)

// fakeScript is a stand-in for ffmpeg and ffprobe that reports its arguments
// and fails when its last argument is "fail".
const fakeScript = `#!/bin/sh
echo "$(basename "$0"): $*"
echo "log line" >&2
for last; do :; done
if [ "$last" = "fail" ]; then
  exit 3
fi
`

// fakeArgs returns arguments that run command with stand-ins for ffmpeg and
// ffprobe, and the buffers that receive its output.
func fakeArgs(tb testing.TB, command string, args ...string) (Args, *bytes.Buffer, *bytes.Buffer) {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("the stand-ins for ffmpeg are shell scripts")
	}

	dir := tb.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fakeScript), 0o700); err != nil { //nolint:gosec // G306: the script must be executable.
			tb.Fatalf("WriteFile(%q) failed, %s", name, err)
		}
	}
	var stdout, stderr bytes.Buffer
	return Args{Command: command, Args: args, WorkDir: dir, Stdout: &stdout, Stderr: &stderr}, &stdout, &stderr
}

func TestRunFfmpeg(t *testing.T) {
	args, stdout, stderr := fakeArgs(t, "FFmpeg", "-i", "in.mov", "out.mp4")

	if err := Run(args); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	if got, want := stdout.String(), "ffmpeg: -nostats -progress pipe:1 -i in.mov out.mp4\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "log line\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestRunFfprobe(t *testing.T) {
	for _, command := range []string{"ffprobe", "probe"} {
		t.Run(command, func(t *testing.T) {
			args, stdout, _ := fakeArgs(t, command, "-show_format", "in.mov")

			if err := Run(args); err != nil {
				t.Fatalf("Run() failed, %s", err)
			}

			if got, want := stdout.String(), "ffprobe: -show_format in.mov\n"; got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	args, stdout, _ := fakeArgs(t, "version")

	if err := Run(args); err != nil {
		t.Fatalf("Run() failed, %s", err)
	}

	ffmpeg, ffprobe := filepath.Join(args.WorkDir, "ffmpeg"), filepath.Join(args.WorkDir, "ffprobe")
	if got, want := stdout.String(), ffmpeg+"\n  ffmpeg: -version\n"+ffprobe+"\n  ffprobe: -version\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunCommandFails(t *testing.T) {
	for _, command := range []string{"ffmpeg", "ffprobe"} {
		t.Run(command, func(t *testing.T) {
			args, _, _ := fakeArgs(t, command, "fail")

			if err := Run(args); !errors.Is(err, ffembed.ErrRunFailed) {
				t.Errorf("Run() = %v, want %v", err, ffembed.ErrRunFailed)
			}
		})
	}
}

func TestRunInvalidCommand(t *testing.T) {
	for _, command := range []string{"", "ffplay"} {
		t.Run(command, func(t *testing.T) {
			err := Run(Args{Command: command})

			var argErr *common.ArgError
			if !errors.As(err, &argErr) {
				t.Fatalf("Run() = %v, want *common.ArgError", err)
			}
			if argErr.Arg != "command" {
				t.Errorf("Run() reported %q, want %q", argErr.Arg, "command")
			}
		})
	}
}

func BenchmarkRun(b *testing.B) {
	args, _, _ := fakeArgs(b, "ffprobe", "-version")

	for b.Loop() {
		if err := Run(args); err != nil {
			b.Errorf("Run() failed, %s", err)
		}
	}
}
