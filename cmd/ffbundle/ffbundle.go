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

// Command ffbundle downloads an ffmpeg package and bundles it to be used with ffembed.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/cloudfra/ffembed/internal/common"
	"github.com/cloudfra/ffembed/internal/ffbundle"
)

var (
	architectureFlag = flag.String("arch", "", "Architecture of the ffmpeg package to download (e.g. amd64, arm64)")
	osFlag           = flag.String("os", "", "Operating system of the ffmpeg package to download (e.g. linux, windows, darwin)")
	inputFlag        = flag.String("input", "", "Input of the ffmpeg package to download (e.g. https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz) for multiple files, delimit by commas.")
	outputFlag       = flag.String("output", "", "Path of the output file, must end in .zip (e.g. ffmpeg-static.zip)")
	hashFlag         = flag.String("hash", "", "Expected hash of the remote file to verify integrity (e.g. sha256:abc123...)")
)

func main() {
	flag.Parse()

	if err := ffbundle.Run(ffbundle.Args{
		Architecture:    *architectureFlag,
		OperatingSystem: *osFlag,
		Input:           common.CSVToList(*inputFlag),
		Output:          *outputFlag,
		Hash:            *hashFlag,
	}); err != nil {
		slog.Error("ERROR", "error", err)
		os.Exit(1)
	}
}
