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
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	internalFfembed "github.com/cloudfra/ffembed/internal/ffembed"
	pb "github.com/cloudfra/ffembed/proto"
)

// fakeFfmpeg is a stand-in for ffmpeg that reports its arguments, a log line,
// and two progress reports. It fails when its last argument is "fail" and
// does not exit by itself when it is "hang".
const fakeFfmpeg = `#!/bin/sh
echo "args: $*"
echo "log line" >&2
echo "frame=10"
echo "fps=25.5"
echo "bitrate= 128.0kbits/s"
echo "total_size=N/A"
echo "out_time_us=400000"
echo "speed=1.5x"
echo "progress=continue"
echo "frame=20"
echo "total_size=2048"
echo "dup_frames=1"
echo "drop_frames=2"
echo "progress=end"
for last; do :; done
if [ "$last" = "fail" ]; then
  echo "it broke" >&2
  exit 3
fi
if [ "$last" = "hang" ]; then
  exec sleep 60
fi
`

// fakeFfprobe is a stand-in for ffprobe that reports what it is given. Every
// input lasts 0.8 seconds, except for "live" which has no duration.
const fakeFfprobe = `#!/bin/sh
for last; do :; done
if [ "$last" = "fail" ]; then
  echo "it broke" >&2
  exit 3
fi
if [ "$3" = "-show_entries" ]; then
  if [ "$last" = "live" ]; then
    echo "N/A"
  else
    echo "0.800000"
  fi
elif [ "$1" = "-v" ]; then
  echo '{"format": {"filename": "'"$last"'", "duration": "1.5", "size": "2048", "nb_streams": 1}, "streams": [{"index": 0, "codec_name": "aac", "sample_rate": "44100", "bit_rate": "N/A"}]}'
else
  echo "args: $*"
fi
`

// installFakes writes the stand-ins for ffmpeg and ffprobe into a directory.
func installFakes(tb testing.TB) string {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("the stand-ins for ffmpeg are shell scripts")
	}

	dir := tb.TempDir()
	for name, script := range map[string]string{"ffmpeg": fakeFfmpeg, "ffprobe": fakeFfprobe} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the script must be executable.
			tb.Fatalf("WriteFile(%q) failed, %s", name, err)
		}
	}
	return dir
}

// zipOf returns a zip archive holding files and its checksum.
func zipOf(tb testing.TB, files map[string]string) (archive []byte, checksum string) {
	tb.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			tb.Fatalf("Create(%q) failed, %s", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			tb.Fatalf("Write(%q) failed, %s", name, err)
		}
	}
	if err := w.Close(); err != nil {
		tb.Fatalf("Close() failed, %s", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), "sha256:" + hex.EncodeToString(sum[:])
}

// serve returns the URL of a server that serves content at every path and
// the number of requests it received.
func serve(tb testing.TB, content []byte) (url string, requests *int) {
	tb.Helper()

	requests = new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*requests++
		if _, err := w.Write(content); err != nil {
			tb.Errorf("Write() failed, %s", err)
		}
	}))
	tb.Cleanup(server.Close)
	return server.URL, requests
}

// testSource returns a source for linux/amd64 that has no ffmpeg anywhere.
func testSource(tb testing.TB) source {
	tb.Helper()

	cacheDir := tb.TempDir()
	return source{
		goos:     "linux",
		goarch:   "amd64",
		cacheDir: func() (string, error) { return cacheDir, nil },
		lookPath: func(string) (string, error) { return "", fs.ErrNotExist },
		embedded: func() ([]byte, error) { return nil, internalFfembed.ErrNotEmbedded },
		manifest: func() (internalFfembed.Manifest, error) { return internalFfembed.Manifest{}, nil },
		client:   http.DefaultClient,
	}
}

// assertBinaries fails the test unless pkg runs the executables in dir with
// the given content.
func assertBinaries(tb testing.TB, pkg FFMpegPackage, dir string, ffmpeg string, ffprobe string) {
	tb.Helper()

	for path, want := range map[string]string{pkg.FFMpegBinary(): ffmpeg, pkg.FFProbeBinary(): ffprobe} {
		if filepath.Dir(path) != dir {
			tb.Errorf("binary %q is not in %q", path, dir)
		}
		got, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			tb.Fatalf("ReadFile(%q) failed, %s", path, err)
		}
		if string(got) != want {
			tb.Errorf("%q holds %q, want %q", path, got, want)
		}
	}
	if filepath.Base(pkg.FFMpegBinary()) != "ffmpeg" || filepath.Base(pkg.FFProbeBinary()) != "ffprobe" {
		tb.Errorf("binaries are %q and %q, want ffmpeg and ffprobe", pkg.FFMpegBinary(), pkg.FFProbeBinary())
	}
}

func TestNewUnavailable(t *testing.T) {
	s := testSource(t)

	testCases := []struct {
		name string
		args *pb.Args
	}{
		{name: "nil args"},
		{name: "empty args", args: &pb.Args{}},
		{name: "prefer installed without an install", args: pb.Args_builder{PreferInstalled: new(true)}.Build()},
		{name: "download without a build for the platform", args: pb.Args_builder{AllowDownload: new(true), AcceptLicense: []string{"GPL-2.0"}}.Build()},
		{name: "license without download", args: pb.Args_builder{AcceptLicense: []string{"GPL-2.0"}}.Build()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.newPackage(t.Context(), tc.args); !errors.Is(err, ErrUnavailable) {
				t.Errorf("newPackage() = %v, want %v", err, ErrUnavailable)
			}
		})
	}
}

func TestNewDefaultWorkDir(t *testing.T) {
	s := testSource(t)
	s.embedded = func() ([]byte, error) { return nil, errors.New("embedded must not be read") }
	cacheDir, err := s.cacheDir()
	if err != nil {
		t.Fatalf("cacheDir() failed, %s", err)
	}
	dir := filepath.Join(cacheDir, "ffembed", "linux_amd64")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) failed, %s", dir, err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) failed, %s", name, err)
		}
	}

	pkg, err := s.newPackage(t.Context(), nil)
	if err != nil {
		t.Fatalf("newPackage() failed, %s", err)
	}

	assertBinaries(t, pkg, dir, "ffmpeg", "ffprobe")
}

func TestNewNoCacheDir(t *testing.T) {
	s := testSource(t)
	s.cacheDir = func() (string, error) { return "", errors.New("no home") }

	if _, err := s.newPackage(t.Context(), nil); err == nil {
		t.Error("newPackage() succeeded, want error")
	}
}

func TestNewPreferInstalled(t *testing.T) {
	s := testSource(t)
	s.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	s.embedded = func() ([]byte, error) { return nil, errors.New("embedded must not be read") }

	pkg, err := s.newPackage(t.Context(), pb.Args_builder{PreferInstalled: new(true), WorkDir: new(t.TempDir())}.Build())
	if err != nil {
		t.Fatalf("newPackage() failed, %s", err)
	}

	if got, want := pkg.FFMpegBinary(), "/usr/bin/ffmpeg"; got != want {
		t.Errorf("FFMpegBinary() = %q, want %q", got, want)
	}
	if got, want := pkg.FFProbeBinary(), "/usr/bin/ffprobe"; got != want {
		t.Errorf("FFProbeBinary() = %q, want %q", got, want)
	}
}

func TestNewPreferInstalledNeedsBothBinaries(t *testing.T) {
	s := testSource(t)
	s.lookPath = func(name string) (string, error) {
		if name == "ffprobe" {
			return "", fs.ErrNotExist
		}
		return "/usr/bin/" + name, nil
	}

	_, err := s.newPackage(t.Context(), pb.Args_builder{PreferInstalled: new(true)}.Build())
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("newPackage() = %v, want %v", err, ErrUnavailable)
	}
}

func TestNewInstalledIsNotUsedUnlessPreferred(t *testing.T) {
	s := testSource(t)
	s.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	if _, err := s.newPackage(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("newPackage() = %v, want %v", err, ErrUnavailable)
	}
}

func TestNewEmbeddedFailure(t *testing.T) {
	s := testSource(t)
	s.embedded = func() ([]byte, error) { return []byte("not a bundle"), nil }

	if _, err := s.newPackage(t.Context(), nil); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("newPackage() = %v, want an install error", err)
	}
}

func TestNewRemoteURL(t *testing.T) {
	archive, checksum := zipOf(t, map[string]string{
		"build/bin/ffmpeg":  "ffmpeg binary",
		"build/bin/ffprobe": "ffprobe binary",
	})
	url, requests := serve(t, archive)
	s := testSource(t)
	s.manifest = func() (internalFfembed.Manifest, error) { return nil, errors.New("manifest must not be read") }
	dir := t.TempDir()
	args := pb.Args_builder{RemoteUrl: new(url + "/ffmpeg.zip"), RemoteUrlChecksum: &checksum, WorkDir: &dir}.Build()

	pkg, err := s.newPackage(t.Context(), args)
	if err != nil {
		t.Fatalf("newPackage() failed, %s", err)
	}
	assertBinaries(t, pkg, dir, "ffmpeg binary", "ffprobe binary")

	// The install is reused rather than downloaded again.
	pkg, err = s.newPackage(t.Context(), args)
	if err != nil {
		t.Fatalf("second newPackage() failed, %s", err)
	}
	assertBinaries(t, pkg, dir, "ffmpeg binary", "ffprobe binary")
	if *requests != 1 {
		t.Errorf("server received %d requests, want 1", *requests)
	}
}

func TestNewRemoteURLChecksumIsOptional(t *testing.T) {
	archive, _ := zipOf(t, map[string]string{"ffmpeg": "ffmpeg binary", "ffprobe": "ffprobe binary"})
	url, _ := serve(t, archive)
	dir := t.TempDir()

	pkg, err := testSource(t).newPackage(t.Context(), pb.Args_builder{RemoteUrl: new(url + "/ffmpeg.zip"), WorkDir: &dir}.Build())
	if err != nil {
		t.Fatalf("newPackage() failed, %s", err)
	}

	assertBinaries(t, pkg, dir, "ffmpeg binary", "ffprobe binary")
}

func TestNewRemoteURLFailures(t *testing.T) {
	complete, _ := zipOf(t, map[string]string{"ffmpeg": "ffmpeg binary", "ffprobe": "ffprobe binary"})
	incomplete, incompleteChecksum := zipOf(t, map[string]string{"ffmpeg": "ffmpeg binary"})
	// Only the Windows executables, which linux cannot run.
	windows, windowsChecksum := zipOf(t, map[string]string{"ffmpeg.exe": "ffmpeg binary", "ffprobe.exe": "ffprobe binary"})

	testCases := []struct {
		name     string
		archive  []byte
		checksum string
	}{
		{name: "checksum mismatch", archive: complete, checksum: incompleteChecksum},
		{name: "ffprobe is missing", archive: incomplete, checksum: incompleteChecksum},
		{name: "build for another platform", archive: windows, checksum: windowsChecksum},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := serve(t, tc.archive)
			args := pb.Args_builder{RemoteUrl: new(url + "/ffmpeg.zip"), RemoteUrlChecksum: &tc.checksum, WorkDir: new(t.TempDir())}.Build()

			if _, err := testSource(t).newPackage(t.Context(), args); err == nil {
				t.Error("newPackage() succeeded, want error")
			}
		})
	}
}

func TestNewManifestDownload(t *testing.T) {
	gpl, gplChecksum := zipOf(t, map[string]string{"ffmpeg": "gpl ffmpeg", "ffprobe": "gpl ffprobe"})
	lgpl, lgplChecksum := zipOf(t, map[string]string{"ffmpeg": "lgpl ffmpeg", "ffprobe": "lgpl ffprobe", "LICENSE": "lgpl"})
	gplURL, _ := serve(t, gpl)
	lgplURL, _ := serve(t, lgpl)
	s := testSource(t)
	s.manifest = func() (internalFfembed.Manifest, error) {
		return internalFfembed.Manifest{"linux_amd64": {
			"gpl":  {License: "GPL-2.0", Files: []internalFfembed.File{{File: gplURL + "/ffmpeg.zip", Checksum: gplChecksum}}},
			"lgpl": {License: "LGPL-2.0", Files: []internalFfembed.File{{File: lgplURL + "/ffmpeg.zip", Checksum: lgplChecksum}}},
		}}, nil
	}

	t.Run("accepted license", func(t *testing.T) {
		dir := t.TempDir()

		pkg, err := s.newPackage(t.Context(), pb.Args_builder{AllowDownload: new(true), AcceptLicense: []string{"LGPL-2.0"}, WorkDir: &dir}.Build())
		if err != nil {
			t.Fatalf("newPackage() failed, %s", err)
		}

		assertBinaries(t, pkg, dir, "lgpl ffmpeg", "lgpl ffprobe")
		if _, err := os.Stat(filepath.Join(dir, "license")); err != nil {
			t.Errorf("license was not installed, %s", err)
		}
	})

	t.Run("no accepted license", func(t *testing.T) {
		for _, accept := range [][]string{nil, {"MIT"}} {
			args := pb.Args_builder{AllowDownload: new(true), AcceptLicense: accept, WorkDir: new(t.TempDir())}.Build()
			if _, err := s.newPackage(t.Context(), args); !errors.Is(err, ErrUnavailable) {
				t.Errorf("newPackage() accepting %q = %v, want %v", accept, err, ErrUnavailable)
			}
		}
	})

	t.Run("download is not allowed", func(t *testing.T) {
		args := pb.Args_builder{AcceptLicense: []string{"LGPL-2.0"}, WorkDir: new(t.TempDir())}.Build()
		if _, err := s.newPackage(t.Context(), args); !errors.Is(err, ErrUnavailable) {
			t.Errorf("newPackage() = %v, want %v", err, ErrUnavailable)
		}
	})

	t.Run("manifest file without checksum", func(t *testing.T) {
		s := s
		s.manifest = func() (internalFfembed.Manifest, error) {
			return internalFfembed.Manifest{"linux_amd64": {
				"gpl": {License: "GPL-2.0", Files: []internalFfembed.File{{File: gplURL + "/ffmpeg.zip"}}},
			}}, nil
		}
		args := pb.Args_builder{AllowDownload: new(true), AcceptLicense: []string{"GPL-2.0"}, WorkDir: new(t.TempDir())}.Build()
		if _, err := s.newPackage(t.Context(), args); err == nil {
			t.Error("newPackage() succeeded, want error")
		}
	})
}

func TestNew(t *testing.T) {
	dir := installFakes(t)

	pkg, err := New(pb.Args_builder{WorkDir: &dir}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}

	if got, want := pkg.FFMpegBinary(), filepath.Join(dir, "ffmpeg"); got != want {
		t.Errorf("FFMpegBinary() = %q, want %q", got, want)
	}
	if got, want := pkg.FFProbeBinary(), filepath.Join(dir, "ffprobe"); got != want {
		t.Errorf("FFProbeBinary() = %q, want %q", got, want)
	}
}

func TestFfmpegArgs(t *testing.T) {
	testCases := []struct {
		name string
		req  *pb.FfmpegRequest
		// want is nil when the request is invalid.
		want []string
	}{
		{
			name: "raw args",
			req:  pb.FfmpegRequest_builder{Args: []string{"-i", "in.mov", "out.mp4"}}.Build(),
			want: []string{"-i", "in.mov", "out.mp4"},
		},
		{
			name: "input and output",
			req:  pb.FfmpegRequest_builder{Inputs: []string{"in.mov"}, Output: new("out.mp4")}.Build(),
			want: []string{"-n", "-i", "in.mov", "out.mp4"},
		},
		{
			name: "every field",
			req: pb.FfmpegRequest_builder{
				Inputs:       []string{"video.mov", "audio.wav"},
				Output:       new("out.mkv"),
				VideoCodec:   new("libx264"),
				AudioCodec:   new("aac"),
				Crf:          new(int32(23)),
				Preset:       new("veryfast"),
				VideoBitrate: new("2M"),
				AudioBitrate: new("128k"),
				Format:       new("matroska"),
				Overwrite:    new(true),
				Faststart:    new(true),
			}.Build(),
			want: []string{"-y", "-i", "video.mov", "-i", "audio.wav", "-c:v", "libx264", "-c:a", "aac", "-crf", "23", "-preset", "veryfast", "-b:v", "2M", "-b:a", "128k", "-f", "matroska", "-movflags", "+faststart", "out.mkv"},
		},
		{
			name: "lossless crf",
			req:  pb.FfmpegRequest_builder{Inputs: []string{"in.mov"}, Output: new("out.mp4"), Crf: new(int32(0))}.Build(),
			want: []string{"-n", "-i", "in.mov", "-crf", "0", "out.mp4"},
		},
		{
			name: "output that looks like an option",
			req:  pb.FfmpegRequest_builder{Inputs: []string{"in.mov"}, Output: new("-out.mp4")}.Build(),
			want: []string{"-n", "-i", "in.mov", "./-out.mp4"},
		},
		{name: "nil request"},
		{name: "empty request", req: &pb.FfmpegRequest{}},
		{name: "no input", req: pb.FfmpegRequest_builder{Output: new("out.mp4")}.Build()},
		{name: "no output", req: pb.FfmpegRequest_builder{Inputs: []string{"in.mov"}}.Build()},
		{name: "args and inputs", req: pb.FfmpegRequest_builder{Args: []string{"-version"}, Inputs: []string{"in.mov"}}.Build()},
		{name: "args and overwrite", req: pb.FfmpegRequest_builder{Args: []string{"-version"}, Overwrite: new(false)}.Build()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ffmpegArgs(tc.req)
			if tc.want == nil {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Errorf("ffmpegArgs() = %q, %v, want %v", got, err, ErrInvalidRequest)
				}
				return
			}
			if err != nil {
				t.Fatalf("ffmpegArgs() failed, %s", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("ffmpegArgs() = %q, want %q", got, tc.want)
			}
		})
	}
}

// runFfmpeg runs the stand-in for ffmpeg and returns its events and response.
func runFfmpeg(t *testing.T, args ...string) ([]*pb.FfmpegEvent, *pb.FfmpegResponse, error) {
	t.Helper()

	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}
	instance, err := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: args}.Build())
	if err != nil {
		t.Fatalf("FFMpeg() failed, %s", err)
	}
	if got := instance.GetBinary(); got != pkg.FFMpegBinary() {
		t.Errorf("GetBinary() = %q, want %q", got, pkg.FFMpegBinary())
	}

	if got := instance.Response(); got != nil {
		t.Errorf("Response() before Wait() = %v, want nil", got)
	}

	var events []*pb.FfmpegEvent
	instance.OnChange(func(event *pb.FfmpegEvent) {
		events = append(events, event)
	})
	err = instance.Wait()
	if again := instance.Wait(); !errors.Is(again, err) {
		t.Errorf("second Wait() = %v, want %v", again, err)
	}
	response := instance.Response()
	if response == nil {
		t.Fatal("Response() after Wait() = nil")
	}
	if len(events) == 0 || events[len(events)-1].GetResponse() != response || events[len(events)-1].GetState() != response.GetState() {
		t.Errorf("last event of %v does not hold the response %v", events, response)
	}
	return events, response, err
}

func TestFFMpeg(t *testing.T) {
	events, response, err := runFfmpeg(t, "-i", "in.mov", "out.mp4")
	if err != nil {
		t.Fatalf("Wait() failed, %s", err)
	}
	if response.GetState() != pb.FfmpegState_FFMPEG_STATE_COMPLETED || response.GetExitCode() != 0 || response.HasLog() ||
		response.GetProgressReports() != 2 || response.GetLastFrame() != 20 || response.GetMaxFps() != 25.5 {
		t.Errorf("response = %v, want completed with exit code 0, 2 progress reports, last frame 20, max fps 25.5", response)
	}

	var (
		output, logs []string
		progress     []*pb.FfmpegProgress
	)
	for i, event := range events {
		wantState := pb.FfmpegState_FFMPEG_STATE_RUNNING
		if i == len(events)-1 {
			wantState = pb.FfmpegState_FFMPEG_STATE_COMPLETED
		}
		if event.GetState() != wantState {
			t.Errorf("event %d has state %s, want %s", i, event.GetState(), wantState)
		}
		switch {
		case event.HasOutput():
			output = append(output, event.GetOutput())
		case event.HasLog():
			logs = append(logs, event.GetLog())
		case event.HasProgress():
			progress = append(progress, event.GetProgress())
		}
	}

	if want := []string{"args: -nostats -progress pipe:1 -i in.mov out.mp4"}; !slices.Equal(output, want) {
		t.Errorf("output = %q, want %q", output, want)
	}
	if want := []string{"log line"}; !slices.Equal(logs, want) {
		t.Errorf("logs = %q, want %q", logs, want)
	}
	if len(progress) != 2 {
		t.Fatalf("got %d progress reports, want 2", len(progress))
	}

	first := progress[0]
	if first.GetFrame() != 10 || first.GetFps() != 25.5 || first.GetBitrateKbps() != 128 || first.GetOutTimeUs() != 400000 || first.GetSpeed() != 1.5 {
		t.Errorf("first progress = %v, want frame 10, fps 25.5, bitrate 128, out time 400000, speed 1.5", first)
	}
	// The stand-in for ffprobe reports the input to last 0.8 seconds.
	if first.GetPercent() != 50 {
		t.Errorf("first progress is at %v percent, want 50", first.GetPercent())
	}
	if second := progress[1]; second.HasPercent() {
		t.Errorf("second progress is at %v percent, want it unset without an output time", second.GetPercent())
	}
	if first.HasTotalSize() {
		t.Errorf("first progress has total size %d, want it unset for N/A", first.GetTotalSize())
	}
	second := progress[1]
	if second.GetFrame() != 20 || second.GetTotalSize() != 2048 || second.GetDupFrames() != 1 || second.GetDropFrames() != 2 {
		t.Errorf("second progress = %v, want frame 20, total size 2048, 1 dup frame, 2 drop frames", second)
	}
	if second.HasFps() {
		t.Errorf("second progress has fps %v, want only what its report held", second.GetFps())
	}
}

func TestFFMpegPercentUnknown(t *testing.T) {
	for _, input := range []string{"live", "-", "pipe:0"} {
		t.Run(input, func(t *testing.T) {
			events, _, err := runFfmpeg(t, "-i", input, "out.mp4")
			if err != nil {
				t.Fatalf("Wait() failed, %s", err)
			}
			for _, event := range events {
				if event.GetProgress().HasPercent() {
					t.Errorf("progress is at %v percent, want it unset", event.GetProgress().GetPercent())
				}
			}
		})
	}
}

func TestFFMpegFailure(t *testing.T) {
	_, response, err := runFfmpeg(t, "fail")

	if !errors.Is(err, ErrRunFailed) || errors.Is(err, ErrCanceled) || errors.Is(err, ErrFailedToStart) {
		t.Fatalf("Wait() = %v, want only %v", err, ErrRunFailed)
	}
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Wait() = %v, want *RunError", err)
	}
	if runErr.Name != "ffmpeg" || filepath.Base(runErr.Binary) != "ffmpeg" || runErr.ExitCode != 3 || runErr.Hint != "" {
		t.Errorf("RunError = %+v, want ffmpeg with exit code 3 and no hint", runErr)
	}
	if !strings.Contains(runErr.Log, "it broke") || !strings.Contains(err.Error(), "it broke") {
		t.Errorf("Wait() = %q, want it to hold the log of ffmpeg", err)
	}
	if response.GetState() != pb.FfmpegState_FFMPEG_STATE_FAILED || response.GetExitCode() != 3 || !strings.Contains(response.GetLog(), "it broke") {
		t.Errorf("response = %v, want failed with exit code 3 and the log of ffmpeg", response)
	}
}

func TestFFMpegCancel(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}
	instance, err := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: []string{"hang"}}.Build())
	if err != nil {
		t.Fatalf("FFMpeg() failed, %s", err)
	}
	// Canceled once ffmpeg reports progress, which is before it hangs.
	instance.OnChange(func(event *pb.FfmpegEvent) {
		if event.HasProgress() {
			instance.Cancel()
		}
	})

	err = instance.Wait()
	instance.Cancel()

	if !errors.Is(err, ErrCanceled) || errors.Is(err, ErrRunFailed) {
		t.Errorf("Wait() = %v, want only %v", err, ErrCanceled)
	}
	if got := instance.Response().GetState(); got != pb.FfmpegState_FFMPEG_STATE_CANCELED {
		t.Errorf("response state = %s, want %s", got, pb.FfmpegState_FFMPEG_STATE_CANCELED)
	}
}

func TestFFMpegCancelBeforeWait(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}
	instance, err := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: []string{"hang"}}.Build())
	if err != nil {
		t.Fatalf("FFMpeg() failed, %s", err)
	}

	instance.Cancel()

	if err := instance.Wait(); !errors.Is(err, ErrCanceled) {
		t.Errorf("Wait() = %v, want %v", err, ErrCanceled)
	}
}

func TestFFMpegCancelAfterCompletion(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}
	instance, err := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: []string{"-version"}}.Build())
	if err != nil {
		t.Fatalf("FFMpeg() failed, %s", err)
	}
	// Canceled on the last event, when ffmpeg has already exited.
	instance.OnChange(func(event *pb.FfmpegEvent) {
		if event.HasResponse() {
			instance.Cancel()
		}
	})

	if err := instance.Wait(); err != nil {
		t.Errorf("Wait() failed, %s", err)
	}
	if got := instance.Response().GetState(); got != pb.FfmpegState_FFMPEG_STATE_COMPLETED {
		t.Errorf("response state = %s, want %s", got, pb.FfmpegState_FFMPEG_STATE_COMPLETED)
	}
}

func TestFFMpegWithoutOnChange(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}
	instance, err := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: []string{"-version"}}.Build())
	if err != nil {
		t.Fatalf("FFMpeg() failed, %s", err)
	}

	if err := instance.Wait(); err != nil {
		t.Errorf("Wait() failed, %s", err)
	}
}

func TestFFMpegInvalid(t *testing.T) {
	pkg := &ffmpegPackage{ffmpeg: filepath.Join(t.TempDir(), "missing"), ffprobe: filepath.Join(t.TempDir(), "missing")}

	if _, err := pkg.FFMpeg(&pb.FfmpegRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("FFMpeg() with an empty request = %v, want %v", err, ErrInvalidRequest)
	}
	if _, err := pkg.FFProbe(&pb.FfprobeRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("FFProbe() with an empty request = %v, want %v", err, ErrInvalidRequest)
	}

	_, ffmpegErr := pkg.FFMpeg(pb.FfmpegRequest_builder{Args: []string{"-version"}}.Build())
	_, ffprobeErr := pkg.FFProbe(pb.FfprobeRequest_builder{Args: []string{"-version"}}.Build())
	_, ffmpegVersionErr := pkg.FFMpegVersion()
	_, ffprobeVersionErr := pkg.FFProbeVersion()
	for name, err := range map[string]error{"FFMpeg()": ffmpegErr, "FFProbe()": ffprobeErr, "FFMpegVersion()": ffmpegVersionErr, "FFProbeVersion()": ffprobeVersionErr} {
		var runErr *RunError
		if !errors.Is(err, ErrFailedToStart) || !errors.As(err, &runErr) {
			t.Errorf("%s with a missing binary = %v, want a *RunError that is %v", name, err, ErrFailedToStart)
			continue
		}
		if runErr.ExitCode != -1 || runErr.Hint != "" {
			t.Errorf("%s with a missing binary = %+v, want exit code -1 and no hint", name, runErr)
		}
	}
}

func TestVersion(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}

	// The stand-ins report their arguments rather than a version.
	got, err := pkg.FFMpegVersion()
	if err != nil {
		t.Fatalf("FFMpegVersion() failed, %s", err)
	}
	if want := "args: -version"; got != want {
		t.Errorf("FFMpegVersion() = %q, want %q", got, want)
	}
	got, err = pkg.FFProbeVersion()
	if err != nil {
		t.Fatalf("FFProbeVersion() failed, %s", err)
	}
	if want := "args: -version"; got != want {
		t.Errorf("FFProbeVersion() = %q, want %q", got, want)
	}
}

func TestFirstInput(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "no args"},
		{name: "no input", args: []string{"-version"}},
		{name: "input", args: []string{"-y", "-i", "in.mov", "out.mp4"}, want: "in.mov"},
		{name: "first of two inputs", args: []string{"-i", "a.mov", "-i", "b.wav", "out.mp4"}, want: "a.mov"},
		{name: "option without a value", args: []string{"-y", "-i"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstInput(tc.args); got != tc.want {
				t.Errorf("firstInput(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestHints(t *testing.T) {
	if got := missingLibraryHint("windows", statusDLLNotFound); got == "" {
		t.Error("missingLibraryHint() for a missing DLL on windows is empty")
	}
	if got := missingLibraryHint("windows", 1); got != "" {
		t.Errorf("missingLibraryHint() for exit code 1 = %q, want none", got)
	}
	if got := missingLibraryHint("linux", statusDLLNotFound); got != "" {
		t.Errorf("missingLibraryHint() on linux = %q, want none", got)
	}

	binary := filepath.Join(t.TempDir(), "ffmpeg")
	if got := missingLoaderHint("linux", binary, fs.ErrNotExist); got != "" {
		t.Errorf("missingLoaderHint() for a missing binary = %q, want none", got)
	}
	if err := os.WriteFile(binary, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) failed, %s", binary, err)
	}
	if got := missingLoaderHint("linux", binary, fs.ErrNotExist); got == "" {
		t.Error("missingLoaderHint() for a binary that exists but is reported missing is empty")
	}
	if got := missingLoaderHint("linux", binary, fs.ErrPermission); got != "" {
		t.Errorf("missingLoaderHint() for a permission error = %q, want none", got)
	}
	if got := missingLoaderHint("windows", binary, fs.ErrNotExist); got != "" {
		t.Errorf("missingLoaderHint() on windows = %q, want none", got)
	}
}

func TestRunErrorError(t *testing.T) {
	err := newRunError("ffmpeg", "/bin/ffmpeg", fs.ErrNotExist, "last log line", false)
	err.Hint = "install it"

	want := "ffmpeg failed to start, file does not exist\nlast log line\nto fix: install it"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(%v, fs.ErrNotExist) = false, want true", err)
	}
}

func TestForEachLine(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: ""},
		{name: "lines", input: "a\nb\n", want: []string{"a", "b"}},
		{name: "no trailing newline", input: "a\nb", want: []string{"a", "b"}},
		{name: "windows line endings", input: "a\r\nb\r\n", want: []string{"a", "b"}},
		{name: "carriage returns split lines", input: "a\rb\rc\n", want: []string{"a", "b", "c"}},
		{name: "empty lines are dropped", input: "a\n\n\r\nb\n", want: []string{"a", "b"}},
		{name: "long line", input: strings.Repeat("x", 1<<20) + "\n", want: []string{strings.Repeat("x", 1<<20)}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			forEachLine(strings.NewReader(tc.input), func(line string) {
				got = append(got, line)
			})
			if !slices.Equal(got, tc.want) {
				t.Errorf("forEachLine() read %d lines, want %d", len(got), len(tc.want))
			}
		})
	}
}

func TestFfprobeArgs(t *testing.T) {
	testCases := []struct {
		name string
		req  *pb.FfprobeRequest
		// want is nil when the request is invalid.
		want           []string
		wantStructured bool
	}{
		{
			name: "raw args",
			req:  pb.FfprobeRequest_builder{Args: []string{"-show_format", "in.mov"}}.Build(),
			want: []string{"-show_format", "in.mov"},
		},
		{
			name:           "input shows format and streams",
			req:            pb.FfprobeRequest_builder{Input: new("in.mov")}.Build(),
			want:           []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-i", "in.mov"},
			wantStructured: true,
		},
		{
			name:           "only streams",
			req:            pb.FfprobeRequest_builder{Input: new("in.mov"), ShowStreams: new(true), SelectStreams: new("v:0")}.Build(),
			want:           []string{"-v", "error", "-print_format", "json", "-show_streams", "-select_streams", "v:0", "-i", "in.mov"},
			wantStructured: true,
		},
		{
			name:           "only chapters",
			req:            pb.FfprobeRequest_builder{Input: new("in.mov"), ShowChapters: new(true)}.Build(),
			want:           []string{"-v", "error", "-print_format", "json", "-show_chapters", "-i", "in.mov"},
			wantStructured: true,
		},
		{
			name:           "everything",
			req:            pb.FfprobeRequest_builder{Input: new("in.mov"), ShowFormat: new(true), ShowStreams: new(true), ShowChapters: new(true)}.Build(),
			want:           []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", "-i", "in.mov"},
			wantStructured: true,
		},
		{name: "nil request"},
		{name: "empty request", req: &pb.FfprobeRequest{}},
		{name: "no input", req: pb.FfprobeRequest_builder{ShowFormat: new(true)}.Build()},
		{name: "args and input", req: pb.FfprobeRequest_builder{Args: []string{"-version"}, Input: new("in.mov")}.Build()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, structured, err := ffprobeArgs(tc.req)
			if tc.want == nil {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Errorf("ffprobeArgs() = %q, %v, want %v", got, err, ErrInvalidRequest)
				}
				return
			}
			if err != nil {
				t.Fatalf("ffprobeArgs() failed, %s", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("ffprobeArgs() = %q, want %q", got, tc.want)
			}
			if structured != tc.wantStructured {
				t.Errorf("ffprobeArgs() structured = %t, want %t", structured, tc.wantStructured)
			}
		})
	}
}

func TestParseFfprobe(t *testing.T) {
	// Trimmed output of ffprobe -print_format json -show_format -show_streams -show_chapters.
	const output = `{
		"streams": [
			{"index": 0, "codec_name": "h264", "codec_long_name": "H.264", "profile": "High", "codec_type": "video",
			 "codec_tag_string": "avc1", "level": 40, "bits_per_sample": 8,
			 "width": 1920, "height": 1080, "pix_fmt": "yuv420p", "r_frame_rate": "30000/1001", "avg_frame_rate": "30000/1001",
			 "duration": "10.010000", "bit_rate": "4000000", "nb_frames": "300", "tags": {"language": "und"}},
			{"index": 1, "codec_name": "aac", "codec_type": "audio", "sample_rate": "48000", "channels": 2,
			 "channel_layout": "stereo", "duration": "N/A"}
		],
		"chapters": [{"id": 7, "start_time": "0.000000", "end_time": "5.500000", "tags": {"title": "Intro"}}],
		"format": {"filename": "in.mp4", "nb_streams": 2, "format_name": "mov,mp4", "format_long_name": "QuickTime / MOV",
			"start_time": "0.021000", "duration": "10.010000", "size": "5242880", "bit_rate": "4189000", "tags": {"encoder": "Lavf"}}
	}`

	resp, err := parseFfprobe([]byte(output))
	if err != nil {
		t.Fatalf("parseFfprobe() failed, %s", err)
	}

	if resp.GetOutput() != output {
		t.Errorf("Output is %d bytes, want the %d bytes parsed", len(resp.GetOutput()), len(output))
	}
	format := resp.GetFormat()
	if format.GetFilename() != "in.mp4" || format.GetFormatName() != "mov,mp4" || format.GetFormatLongName() != "QuickTime / MOV" ||
		format.GetStartTime() != 0.021 || format.GetDuration() != 10.01 || format.GetSize() != 5242880 ||
		format.GetBitRate() != 4189000 || format.GetNbStreams() != 2 || format.GetTags()["encoder"] != "Lavf" {
		t.Errorf("format = %v, does not match the output", format)
	}
	if len(resp.GetStreams()) != 2 {
		t.Fatalf("got %d streams, want 2", len(resp.GetStreams()))
	}
	video := resp.GetStreams()[0]
	if video.GetIndex() != 0 || video.GetCodecName() != "h264" || video.GetCodecLongName() != "H.264" || video.GetProfile() != "High" ||
		video.GetCodecType() != "video" || video.GetWidth() != 1920 || video.GetHeight() != 1080 || video.GetPixFmt() != "yuv420p" ||
		video.GetRFrameRate() != "30000/1001" || video.GetAvgFrameRate() != "30000/1001" || video.GetDuration() != 10.01 ||
		video.GetBitRate() != 4000000 || video.GetCodecTagString() != "avc1" || video.GetLevel() != 40 || video.GetBitsPerSample() != 8 || video.GetNbFrames() != 300 || video.GetTags()["language"] != "und" {
		t.Errorf("video stream = %v, does not match the output", video)
	}
	audio := resp.GetStreams()[1]
	if audio.GetIndex() != 1 || audio.GetCodecName() != "aac" || audio.GetCodecType() != "audio" || audio.GetSampleRate() != 48000 ||
		audio.GetChannels() != 2 || audio.GetChannelLayout() != "stereo" || audio.GetDuration() != 0 {
		t.Errorf("audio stream = %v, does not match the output", audio)
	}
	if len(resp.GetChapters()) != 1 {
		t.Fatalf("got %d chapters, want 1", len(resp.GetChapters()))
	}
	chapter := resp.GetChapters()[0]
	if chapter.GetId() != 7 || chapter.GetStartTime() != 0 || chapter.GetEndTime() != 5.5 || chapter.GetTags()["title"] != "Intro" {
		t.Errorf("chapter = %v, does not match the output", chapter)
	}
}

func TestParseFfprobeInvalid(t *testing.T) {
	if _, err := parseFfprobe([]byte("not json")); err == nil {
		t.Error("parseFfprobe() succeeded, want error")
	}

	resp, err := parseFfprobe([]byte("{}"))
	if err != nil {
		t.Fatalf("parseFfprobe() of an empty object failed, %s", err)
	}
	if resp.HasFormat() || len(resp.GetStreams()) != 0 || len(resp.GetChapters()) != 0 {
		t.Errorf("parseFfprobe() of an empty object = %v, want nothing set", resp)
	}
}

func TestFFProbe(t *testing.T) {
	pkg, err := New(pb.Args_builder{WorkDir: new(installFakes(t))}.Build())
	if err != nil {
		t.Fatalf("New() failed, %s", err)
	}

	t.Run("structured", func(t *testing.T) {
		resp, err := pkg.FFProbe(pb.FfprobeRequest_builder{Input: new("in.mp4")}.Build())
		if err != nil {
			t.Fatalf("FFProbe() failed, %s", err)
		}
		if got := resp.GetFormat(); got.GetFilename() != "in.mp4" || got.GetDuration() != 1.5 || got.GetSize() != 2048 {
			t.Errorf("format = %v, want in.mp4 of 1.5 seconds and 2048 bytes", got)
		}
		if got := resp.GetStreams(); len(got) != 1 || got[0].GetCodecName() != "aac" || got[0].GetSampleRate() != 44100 || got[0].GetBitRate() != 0 {
			t.Errorf("streams = %v, want one aac stream at 44100 Hz", got)
		}
	})

	t.Run("raw args", func(t *testing.T) {
		resp, err := pkg.FFProbe(pb.FfprobeRequest_builder{Args: []string{"-show_format", "in.mp4"}}.Build())
		if err != nil {
			t.Fatalf("FFProbe() failed, %s", err)
		}
		if got, want := resp.GetOutput(), "args: -show_format in.mp4\n"; got != want {
			t.Errorf("Output = %q, want %q", got, want)
		}
		if resp.HasFormat() {
			t.Errorf("format = %v, want raw output to be left unparsed", resp.GetFormat())
		}
	})

	t.Run("failure", func(t *testing.T) {
		_, err := pkg.FFProbe(pb.FfprobeRequest_builder{Args: []string{"fail"}}.Build())
		var runErr *RunError
		if !errors.Is(err, ErrRunFailed) || !errors.As(err, &runErr) {
			t.Fatalf("FFProbe() = %v, want a *RunError that is %v", err, ErrRunFailed)
		}
		if runErr.Name != "ffprobe" || runErr.ExitCode != 3 || runErr.Log != "it broke" {
			t.Errorf("RunError = %+v, want ffprobe with exit code 3 and its log", runErr)
		}
	})
}
