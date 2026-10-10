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

// Package ffexec runs the ffmpeg or ffprobe provided by ffembed.
package ffexec

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/cloudfra/ffembed"
	"github.com/cloudfra/ffembed/internal/common"
	pb "github.com/cloudfra/ffembed/proto"
)

// Args holds the inputs for Run.
type Args struct {
	// Command to run: ffmpeg, ffprobe, or version.
	Command string
	// Args to pass to the command.
	Args []string
	// PreferInstalled prefers an ffmpeg found on the PATH over the embedded
	// copy or a download.
	PreferInstalled bool
	// AllowDownload allows downloading the ffmpeg build listed in the
	// built-in manifest.
	AllowDownload bool
	// AcceptLicense lists the licenses that are acceptable for a downloaded
	// ffmpeg, in order of preference.
	AcceptLicense []string
	// RemoteURL is the URL of an ffmpeg archive to download instead of the
	// one in the built-in manifest.
	RemoteURL string
	// RemoteURLChecksum is the expected checksum of the file at RemoteURL
	// (e.g. sha256:abc123...)
	RemoteURLChecksum string
	// WorkDir is the directory ffmpeg is installed into and reused from.
	WorkDir string
	// Stdout and Stderr receive the output of the command, os.Stdout and
	// os.Stderr when nil.
	Stdout io.Writer
	Stderr io.Writer
}

// Run runs the command of args with the ffmpeg provided by ffembed and
// forwards its output. It returns a *common.ArgError when the command is
// missing or unknown.
func Run(args Args) error {
	slog.Debug("Running", "command", args.Command, "args", args.Args)
	if args.Stdout == nil {
		args.Stdout = os.Stdout
	}
	if args.Stderr == nil {
		args.Stderr = os.Stderr
	}

	var run func(ffembed.FFMpegPackage, Args) error
	switch strings.ToLower(args.Command) {
	case "":
		return &common.ArgError{Arg: "command", Message: "Command is required, use ffmpeg, ffprobe, or version"}
	case "ffmpeg":
		run = runFfmpeg
	case "probe", "ffprobe":
		run = runFfprobe
	case "version":
		run = runVersion
	default:
		return &common.ArgError{Arg: "command", Message: fmt.Sprintf("Command %q is not supported, use ffmpeg, ffprobe, or version", args.Command)}
	}

	ff, err := ffembed.New(pb.Args_builder{
		PreferInstalled:   &args.PreferInstalled,
		AllowDownload:     &args.AllowDownload,
		AcceptLicense:     args.AcceptLicense,
		RemoteUrl:         &args.RemoteURL,
		RemoteUrlChecksum: &args.RemoteURLChecksum,
		WorkDir:           &args.WorkDir,
	}.Build())
	if err != nil {
		return err
	}
	return run(ff, args)
}

func runFfmpeg(ff ffembed.FFMpegPackage, args Args) error {
	instance, err := ff.FFMpeg(pb.FfmpegRequest_builder{
		Args: args.Args,
	}.Build())
	if err != nil {
		return err
	}
	// A failed write is not worth stopping ffmpeg for, the first one is
	// reported once it is done.
	var writeErr error
	write := func(w io.Writer, line string) {
		if _, err := fmt.Fprintln(w, line); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	instance.OnChange(func(event *pb.FfmpegEvent) {
		switch {
		case event.HasLog():
			write(args.Stderr, event.GetLog())
		case event.HasOutput():
			write(args.Stdout, event.GetOutput())
		case event.HasProgress():
			slog.Info("ffmpeg progress", "progress", event.GetProgress())
		}
	})
	if err := instance.Wait(); err != nil {
		return err
	}
	return writeErr
}

func runFfprobe(ff ffembed.FFMpegPackage, args Args) error {
	resp, err := ff.FFProbe(pb.FfprobeRequest_builder{
		Args: args.Args,
	}.Build())
	if err != nil {
		return err
	}
	_, err = io.WriteString(args.Stdout, resp.GetOutput())
	return err
}

// runVersion reports which ffmpeg and ffprobe are used and their versions.
func runVersion(ff ffembed.FFMpegPackage, args Args) error {
	ffmpegVersion, err := ff.FFMpegVersion()
	if err != nil {
		return err
	}
	ffprobeVersion, err := ff.FFProbeVersion()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(args.Stdout, "%s\n  %s\n%s\n  %s\n", ff.FFMpegBinary(), ffmpegVersion, ff.FFProbeBinary(), ffprobeVersion)
	return err
}
