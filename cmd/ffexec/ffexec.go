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

// Command ffexec runs the ffmpeg or ffprobe provided by ffembed.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/cloudfra/ffembed/internal/app/ffexec"
	"github.com/cloudfra/ffembed/internal/common"
)

const usage = `Usage: ffexec [flags] <command> [arguments...]

ffexec runs ffmpeg or ffprobe. The executables are taken from the first of: an
earlier install in the work directory, the system when -prefer-installed is
set, the bundle embedded in ffexec, or a download when -remote-url or
-allow-download is set.

Commands:
  ffmpeg   Run ffmpeg with the arguments.
  ffprobe  Run ffprobe with the arguments. (alias: probe)
  version  Print which ffmpeg and ffprobe are used and their versions.

Everything after ffmpeg or ffprobe is passed to it unchanged, so the flags of
ffexec must come before the command.

Examples:
  ffexec ffmpeg -i input.mov -c:v libx264 output.mp4
  ffexec -prefer-installed ffprobe -show_format input.mov
  ffexec -allow-download -accept-license LGPL-3.0 ffmpeg -version

Flags:
`

var (
	preferInstalledFlag   = flag.Bool("prefer-installed", false, "Prefer an ffmpeg and ffprobe found on the PATH over the embedded copy or a download")
	allowDownloadFlag     = flag.Bool("allow-download", false, "Allow downloading the ffmpeg build listed in the built-in manifest, requires -accept-license")
	acceptLicenseFlag     = flag.String("accept-license", "", "Licenses that are acceptable for a downloaded ffmpeg, in order of preference and delimited by commas (e.g. LGPL-3.0,GPL-3.0)")
	remoteURLFlag         = flag.String("remote-url", "", "URL of an ffmpeg archive to download instead of the one in the built-in manifest")
	remoteURLChecksumFlag = flag.String("remote-url-checksum", "", "Expected checksum of the file at -remote-url (e.g. sha256:abc123...)")
	workDirFlag           = flag.String("work-dir", "", "Directory ffmpeg is installed into and reused from, defaults to the user's cache directory")
)

func main() {
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage) //nolint:errcheck // Nothing can be done when the usage cannot be written.
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := ffexec.Run(ffexec.Args{
		Command:           flag.Arg(0),
		Args:              flag.Args()[1:],
		PreferInstalled:   *preferInstalledFlag,
		AllowDownload:     *allowDownloadFlag,
		AcceptLicense:     common.CSVToList(*acceptLicenseFlag),
		RemoteURL:         *remoteURLFlag,
		RemoteURLChecksum: *remoteURLChecksumFlag,
		WorkDir:           *workDirFlag,
	}); err != nil {
		slog.Error("ERROR", "error", err)
		os.Exit(1)
	}
}
