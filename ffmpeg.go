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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/cloudfra/ffembed/proto"
)

const (
	// logTailLines is how many of the last log lines of ffmpeg are kept for
	// the error and response of a run that did not complete.
	logTailLines = 10
	// probeTimeout bounds the probe of the input duration, so an input that
	// is slow to open cannot hold back ffmpeg.
	probeTimeout = 2 * time.Second
)

// progressLine matches the key=value lines ffmpeg writes for -progress.
var progressLine = regexp.MustCompile(`^([a-z][a-z0-9_]*)=(.*)$`)

// FFMpeg starts ffmpeg with the arguments of req. Progress is requested on
// stdout, so req must not make ffmpeg write media to stdout.
func (p *ffmpegPackage) FFMpeg(req *pb.FfmpegRequest) (FFMpeg, error) {
	args, err := ffmpegArgs(req)
	if err != nil {
		return nil, err
	}
	// The context is what Cancel stops ffmpeg with.
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, p.ffmpeg, append([]string{"-nostats", "-progress", "pipe:1"}, args...)...) //nolint:gosec // G204: running ffmpeg with caller arguments is the purpose.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	// Probed before ffmpeg starts, so ffmpeg is not held back by it while
	// it already has the output open.
	durationUs := p.inputDurationUs(firstInput(args))
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, newRunError("ffmpeg", p.ffmpeg, err, "", false)
	}
	return &ffmpeg{
		binary:     p.ffmpeg,
		cmd:        cmd,
		stdout:     stdout,
		stderr:     stderr,
		cancel:     cancel,
		output:     req.GetOutput(),
		durationUs: durationUs,
	}, nil
}

// ffmpegArgs returns the command line arguments for req.
func ffmpegArgs(req *pb.FfmpegRequest) ([]string, error) {
	structured := len(req.GetInputs()) > 0 || req.HasOutput() || req.HasVideoCodec() || req.HasAudioCodec() ||
		req.HasCrf() || req.HasPreset() || req.HasVideoBitrate() || req.HasAudioBitrate() || req.HasFormat() ||
		req.HasOverwrite() || req.HasFaststart()
	if len(req.GetArgs()) > 0 {
		if structured {
			return nil, fmt.Errorf("%w, ffmpeg request sets args, no other field can be set", ErrInvalidRequest)
		}
		return req.GetArgs(), nil
	}
	if len(req.GetInputs()) == 0 {
		return nil, fmt.Errorf("%w, ffmpeg request needs args or inputs", ErrInvalidRequest)
	}
	if req.GetOutput() == "" {
		return nil, fmt.Errorf("%w, ffmpeg request needs an output", ErrInvalidRequest)
	}

	// Without -y or -n ffmpeg asks before replacing the output, which
	// cannot be answered.
	args := []string{"-n"}
	if req.GetOverwrite() {
		args = []string{"-y"}
	}
	for _, input := range req.GetInputs() {
		args = append(args, "-i", input)
	}
	for _, option := range []struct {
		flag  string
		set   bool
		value string
	}{
		{"-c:v", req.HasVideoCodec(), req.GetVideoCodec()},
		{"-c:a", req.HasAudioCodec(), req.GetAudioCodec()},
		{"-crf", req.HasCrf(), strconv.Itoa(int(req.GetCrf()))},
		{"-preset", req.HasPreset(), req.GetPreset()},
		{"-b:v", req.HasVideoBitrate(), req.GetVideoBitrate()},
		{"-b:a", req.HasAudioBitrate(), req.GetAudioBitrate()},
		{"-f", req.HasFormat(), req.GetFormat()},
		{"-movflags", req.GetFaststart(), "+faststart"},
	} {
		if option.set {
			args = append(args, option.flag, option.value)
		}
	}
	// A leading "-" would make ffmpeg read the output as an option.
	output := req.GetOutput()
	if strings.HasPrefix(output, "-") {
		output = "./" + output
	}
	return append(args, output), nil
}

// firstInput returns the first input of the ffmpeg arguments args, empty
// when there is none.
func firstInput(args []string) string {
	for i, arg := range args[:max(len(args)-1, 0)] {
		if arg == "-i" {
			return args[i+1]
		}
	}
	return ""
}

// inputDurationUs returns the duration of input in microseconds, which is
// what progress is measured against. It is 0 when the duration is unknown:
// there is no input, it is read from stdin, it has no duration (e.g. a live
// source), or it cannot be probed in time.
func (p *ffmpegPackage) inputDurationUs(input string) int64 {
	if input == "" || input == "-" || strings.HasPrefix(input, "pipe:") {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "-i", input).Output() //nolint:gosec // G204: probing the input the caller gave ffmpeg.
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0
	}
	return int64(seconds * 1e6)
}

// ffmpeg is a started ffmpeg process.
type ffmpeg struct {
	binary string
	cmd    *exec.Cmd
	stdout io.Reader
	stderr io.Reader
	// output is the output file of the request, when it named one.
	output string
	// durationUs is the duration of the first input, 0 when unknown.
	durationUs int64

	// cancel stops the process, cancelled tells that Cancel did so.
	cancel    context.CancelFunc
	cancelled atomic.Bool

	// mu guards onChange and response, and serializes the calls of onChange.
	mu       sync.Mutex
	onChange func(*pb.FfmpegEvent)
	response *pb.FfmpegResponse

	wait sync.Once
	err  error
}

func (f *ffmpeg) GetBinary() string {
	return f.binary
}

func (f *ffmpeg) OnChange(onChange func(*pb.FfmpegEvent)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onChange = onChange
}

func (f *ffmpeg) Cancel() {
	f.cancelled.Store(true)
	f.cancel()
}

func (f *ffmpeg) Response() *pb.FfmpegResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.response
}

func (f *ffmpeg) emit(event *pb.FfmpegEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(event)
	}
}

// Wait reads the output of ffmpeg until it exits. Nothing is read before,
// which holds ffmpeg back and so keeps every event for the OnChange function.
func (f *ffmpeg) Wait() error {
	f.wait.Do(func() {
		f.err = f.run()
	})
	return f.err
}

func (f *ffmpeg) run() error {
	// Releases the context once ffmpeg is done.
	defer f.cancel()

	var (
		wg      sync.WaitGroup
		logTail []string
	)
	response := pb.FfmpegResponse_builder{
		ProgressReports: new(int64(0)),
		LastFrame:       new(int64(0)),
		MaxFps:          new(0.0),
	}.Build()
	if f.output != "" {
		response.SetOutput(f.output)
	}
	wg.Go(func() {
		progress := &pb.FfmpegProgress{}
		forEachLine(f.stdout, func(line string) {
			match := progressLine.FindStringSubmatch(line)
			if match == nil {
				f.emit(pb.FfmpegEvent_builder{State: pb.FfmpegState_FFMPEG_STATE_RUNNING.Enum(), Output: &line}.Build())
				return
			}
			// A report ends with its progress key.
			if match[1] != "progress" {
				setProgress(progress, match[1], match[2])
				return
			}
			if f.durationUs > 0 && progress.HasOutTimeUs() {
				progress.SetPercent(min(max(float64(progress.GetOutTimeUs())/float64(f.durationUs)*100, 0), 100))
			}
			response.SetProgressReports(response.GetProgressReports() + 1)
			response.SetLastFrame(max(response.GetLastFrame(), progress.GetFrame()))
			response.SetMaxFps(max(response.GetMaxFps(), progress.GetFps()))
			f.emit(pb.FfmpegEvent_builder{State: pb.FfmpegState_FFMPEG_STATE_RUNNING.Enum(), Progress: progress}.Build())
			progress = &pb.FfmpegProgress{}
		})
	})
	wg.Go(func() {
		forEachLine(f.stderr, func(line string) {
			if len(logTail) == logTailLines {
				logTail = logTail[1:]
			}
			logTail = append(logTail, line)
			f.emit(pb.FfmpegEvent_builder{State: pb.FfmpegState_FFMPEG_STATE_RUNNING.Enum(), Log: &line}.Build())
		})
	})
	// The pipes must be drained before Wait, which closes them.
	wg.Wait()

	var runErr *RunError
	response.SetState(pb.FfmpegState_FFMPEG_STATE_COMPLETED)
	response.SetExitCode(0)
	// After a cancel that came too late to stop ffmpeg, Wait reports the
	// context of a run that did complete.
	err := f.cmd.Wait()
	if err != nil && (!errors.Is(err, context.Canceled) || !f.cmd.ProcessState.Success()) {
		runErr = newRunError("ffmpeg", f.binary, err, strings.Join(logTail, "\n"), f.cancelled.Load())
		response.SetState(pb.FfmpegState_FFMPEG_STATE_FAILED)
		if errors.Is(runErr, ErrCancelled) {
			response.SetState(pb.FfmpegState_FFMPEG_STATE_CANCELLED)
		}
		response.SetExitCode(int32(runErr.ExitCode)) //nolint:gosec // G115: exit codes fit in 32 bits.
		response.SetLog(runErr.Log)
	}

	f.mu.Lock()
	f.response = response
	f.mu.Unlock()
	f.emit(pb.FfmpegEvent_builder{State: response.GetState().Enum(), Response: response}.Build())
	if runErr != nil {
		return runErr
	}
	return nil
}

// forEachLine calls f with every line of r, without its line ending. ffmpeg
// ends lines it rewrites in place with a carriage return, which are treated
// as lines of their own.
func forEachLine(r io.Reader, f func(line string)) {
	reader := bufio.NewReader(r)
	for {
		text, err := reader.ReadString('\n')
		for line := range strings.SplitSeq(strings.TrimRight(text, "\r\n"), "\r") {
			if line != "" {
				f(line)
			}
		}
		if err != nil {
			// The pipe is closed when ffmpeg exits, any other error also
			// ends the output.
			return
		}
	}
}

// setProgress stores the value ffmpeg reported for key in progress. Unknown
// keys and values ffmpeg does not know yet ("N/A") are ignored.
func setProgress(progress *pb.FfmpegProgress, key string, value string) {
	value = strings.TrimSpace(value)
	switch key {
	case "frame":
		setInt(value, progress.SetFrame)
	case "fps":
		setFloat(value, progress.SetFps)
	case "bitrate":
		setFloat(strings.TrimSuffix(value, "kbits/s"), progress.SetBitrateKbps)
	case "total_size":
		setInt(value, progress.SetTotalSize)
	case "out_time_us":
		setInt(value, progress.SetOutTimeUs)
	case "dup_frames":
		setInt(value, progress.SetDupFrames)
	case "drop_frames":
		setInt(value, progress.SetDropFrames)
	case "speed":
		setFloat(strings.TrimSuffix(value, "x"), progress.SetSpeed)
	}
}

func setInt(value string, set func(int64)) {
	if v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
		set(v)
	}
}

func setFloat(value string, set func(float64)) {
	if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
		set(v)
	}
}
