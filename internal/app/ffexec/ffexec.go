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

// Package ffexec is the starter implementation new services should replace.
package ffexec

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudfra/ffembed"
	pb "github.com/cloudfra/ffembed/proto"
)

// Args holds the inputs for Run.
type Args struct {
	// Command to run.
	Command string
	// Args to pass to the ffmpeg application.
	Args []string
}

// Run executes the ffexec application logic.
func Run(args Args) error {
	slog.Info("Running", "command", args.Command, "args", args.Args)
	command := strings.ToLower(args.Command)
	switch command {
	case "ffmpeg":
		return runFfmpeg(args.Args)
	case "probe", "ffprobe":
		return runFfprobe(args.Args)
	}
	return fmt.Errorf("command %q is not ", args.Command)
}

func runFfmpeg(args []string) error {
	ff, err := ffembed.New(&pb.Args{})
	if err != nil {
		return err
	}
	instance, err := ff.FFMpeg(pb.FfmpegRequest_builder{
		Args: args,
	}.Build())
	if err != nil {
		return err
	}
	instance.OnChange(func(event *pb.FfmpegEvent) {
		slog.Info("ffmpeg progress", "event", event)
	})
	return instance.Wait()
}

func runFfprobe(args []string) error {
	return nil
}
