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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	pb "github.com/cloudfra/ffembed/proto"
)

// FFProbe runs ffprobe with the arguments of req. Requests that do not use
// raw args are answered in JSON, which is parsed into the response.
func (p *ffmpegPackage) FFProbe(req *pb.FfprobeRequest) (*pb.FfprobeResponse, error) {
	args, structured, err := ffprobeArgs(req)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), p.ffprobe, args...) //nolint:gosec // G204: running ffprobe with caller arguments is the purpose.
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, newRunError("ffprobe", p.ffprobe, err, strings.TrimSpace(stderr.String()), false)
	}

	if !structured {
		return pb.FfprobeResponse_builder{Output: new(stdout.String())}.Build(), nil
	}
	return parseFfprobe(stdout.Bytes())
}

// ffprobeArgs returns the command line arguments for req and whether they
// were built from the structured fields rather than the raw args.
func ffprobeArgs(req *pb.FfprobeRequest) (args []string, structured bool, err error) {
	structured = req.HasInput() || req.HasShowFormat() || req.HasShowStreams() || req.HasShowChapters() || req.HasSelectStreams()
	if len(req.GetArgs()) > 0 {
		if structured {
			return nil, false, fmt.Errorf("%w, ffprobe request sets args, no other field can be set", ErrInvalidRequest)
		}
		return req.GetArgs(), false, nil
	}
	if req.GetInput() == "" {
		return nil, false, fmt.Errorf("%w, ffprobe request needs args or an input", ErrInvalidRequest)
	}

	args = []string{"-v", "error", "-print_format", "json"}
	showFormat, showStreams := req.GetShowFormat(), req.GetShowStreams()
	if !showFormat && !showStreams && !req.GetShowChapters() {
		showFormat, showStreams = true, true
	}
	if showFormat {
		args = append(args, "-show_format")
	}
	if showStreams {
		args = append(args, "-show_streams")
	}
	if req.GetShowChapters() {
		args = append(args, "-show_chapters")
	}
	if req.HasSelectStreams() {
		args = append(args, "-select_streams", req.GetSelectStreams())
	}
	// -i keeps an input that starts with "-" from being read as an option.
	return append(args, "-i", req.GetInput()), true, nil
}

// ffprobeJSON is the JSON ffprobe writes. It reports most numbers as strings.
type ffprobeJSON struct {
	Format *struct {
		Filename       string            `json:"filename"`
		FormatName     string            `json:"format_name"`
		FormatLongName string            `json:"format_long_name"`
		StartTime      string            `json:"start_time"`
		Duration       string            `json:"duration"`
		Size           string            `json:"size"`
		BitRate        string            `json:"bit_rate"`
		NbStreams      int32             `json:"nb_streams"`
		Tags           map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Index          int32             `json:"index"`
		CodecTagString string            `json:"codec_tag_string"`
		Level          int32             `json:"level"`
		BitsPerSample  int32             `json:"bits_per_sample"`
		CodecName      string            `json:"codec_name"`
		CodecLongName  string            `json:"codec_long_name"`
		CodecType      string            `json:"codec_type"`
		Profile        string            `json:"profile"`
		Width          int32             `json:"width"`
		Height         int32             `json:"height"`
		PixFmt         string            `json:"pix_fmt"`
		RFrameRate     string            `json:"r_frame_rate"`
		AvgFrameRate   string            `json:"avg_frame_rate"`
		SampleRate     string            `json:"sample_rate"`
		Channels       int32             `json:"channels"`
		ChannelLayout  string            `json:"channel_layout"`
		Duration       string            `json:"duration"`
		BitRate        string            `json:"bit_rate"`
		NbFrames       string            `json:"nb_frames"`
		Tags           map[string]string `json:"tags"`
	} `json:"streams"`
	Chapters []struct {
		ID        int64             `json:"id"`
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

// parseFfprobe converts the JSON output of ffprobe into a response.
func parseFfprobe(output []byte) (*pb.FfprobeResponse, error) {
	var probe ffprobeJSON
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("cannot parse the output of ffprobe, %w", err)
	}

	resp := pb.FfprobeResponse_builder{Output: new(string(output))}.Build()
	if f := probe.Format; f != nil {
		resp.SetFormat(pb.FfprobeFormat_builder{
			Filename:       &f.Filename,
			FormatName:     &f.FormatName,
			FormatLongName: &f.FormatLongName,
			StartTime:      new(toFloat(f.StartTime)),
			Duration:       new(toFloat(f.Duration)),
			Size:           new(toInt(f.Size)),
			BitRate:        new(toInt(f.BitRate)),
			NbStreams:      &f.NbStreams,
			Tags:           f.Tags,
		}.Build())
	}
	streams := make([]*pb.FfprobeStream, 0, len(probe.Streams))
	for _, s := range probe.Streams {
		streams = append(streams, pb.FfprobeStream_builder{
			Index:          &s.Index,
			CodecTagString: &s.CodecTagString,
			Level:          &s.Level,
			BitsPerSample:  &s.BitsPerSample,
			CodecName:      &s.CodecName,
			CodecLongName:  &s.CodecLongName,
			CodecType:      &s.CodecType,
			Profile:        &s.Profile,
			Width:          &s.Width,
			Height:         &s.Height,
			PixFmt:         &s.PixFmt,
			RFrameRate:     &s.RFrameRate,
			AvgFrameRate:   &s.AvgFrameRate,
			SampleRate:     new(int32(toInt(s.SampleRate))), //nolint:gosec // G115: sample rates fit in 32 bits.
			Channels:       &s.Channels,
			ChannelLayout:  &s.ChannelLayout,
			Duration:       new(toFloat(s.Duration)),
			BitRate:        new(toInt(s.BitRate)),
			NbFrames:       new(toInt(s.NbFrames)),
			Tags:           s.Tags,
		}.Build())
	}
	resp.SetStreams(streams)
	chapters := make([]*pb.FfprobeChapter, 0, len(probe.Chapters))
	for _, c := range probe.Chapters {
		chapters = append(chapters, pb.FfprobeChapter_builder{
			Id:        &c.ID,
			StartTime: new(toFloat(c.StartTime)),
			EndTime:   new(toFloat(c.EndTime)),
			Tags:      c.Tags,
		}.Build())
	}
	resp.SetChapters(chapters)
	return resp, nil
}

// toFloat parses a number ffprobe reported as a string, 0 when it is absent
// or unknown ("N/A").
func toFloat(value string) float64 {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return v
}

// toInt parses an integer ffprobe reported as a string, 0 when it is absent
// or unknown ("N/A").
func toInt(value string) int64 {
	v, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
