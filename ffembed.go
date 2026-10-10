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

// Package ffembed
package ffembed

import (
	internalFfembed "github.com/cloudfra/ffembed/internal/ffembed"
	pb "github.com/cloudfra/ffembed/proto"
)

type Args struct {
	UseExternalIfAvailable bool
	// AllowDownload
	AllowDownload bool
	// RemoteUrl is the URL to download the ffmpeg archive. (optional)
	RemoteUrl string
	// Checksum is used to validate the RemoteUrl file contents. (optional)
	RemoteUrlChecksum string
	// AcceptLicense is a list of licenses that are acceptable for ffmpeg.
	AcceptLicense []string
}

type FFMpegPackage interface {
	FFMpegBinary() string
	FFProbeBinary() string
	FFMpeg(*pb.FfmpegRequest) (FFMpeg, error)
	FFProbe(*pb.FfprobeRequest) (*pb.FfprobeResponse, error)
}

type FFMpeg interface {
	GetBinary() string
	OnChange(func(*pb.FfmpegEvent))
	Wait() error
}

type FFProbe interface {
	GetBinary() string
}

func New(args *pb.Args) (FFMpegPackage, error) {
	// TODO: Check if the ffmpeg is installed in the working directory.
	// If there's no ffmpeg installed then run through the following steps.

	// Check for extracted ffmpeg.
	// If not there then use the args to determine what should be done.
	// UseExternalIfAvailable search for the local install. If that's not available then look for embedded.
	// Embedded install.
	// If that's not available look to see if it can be installed.
	// Use an embedded JSON manifest that.
	// Download verifies based on checksum value.
	// All else fails then return error.
	// Use github.com/cloudfra/ufs to download files with urls that are parameterized via ufs.checksum argument appended to download the file.
	_, err := internalFfembed.Ffmpeg()
	if err != nil {
		return nil, err
	}
	// TODO: Extract the archive and prepare it for
	return nil, nil
}
