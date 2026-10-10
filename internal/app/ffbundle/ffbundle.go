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

// Package ffbundle bundles the ffmpeg binaries and license found in one or more
// ffmpeg packages into a single archive that ffembed can consume.
package ffbundle

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfra/ffembed/internal/common"
	"github.com/cloudfra/ufs"
	"github.com/mholt/archives"
	// _ "github.com/cloudfra/ufs/drivers/all"
)

const (
	// outputExtension is the file extension every bundle must have.
	outputExtension = common.TarXzExtension
	// outputFileMode is the permission of the bundle that is written.
	outputFileMode = 0o644
)

// Args holds the inputs for Run.
type Args struct {
	// Architecture of the ffmpeg package to download (e.g. amd64, arm64)
	Architecture string
	// OperatingSystem of the ffmpeg package to download (e.g. linux, windows, darwin)
	OperatingSystem string
	// Input file of the ffmpeg package. Local file or URL to download (e.g. https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz)
	Input []string
	// Path of the output file, which must end in .tar.xz (e.g. ffmpeg-static.tar.xz)
	Output string
	// Hash is the expected hash of the remote file to verify integrity (e.g. sha256:abc123...)
	Hash string
}

// Run collects the ffmpeg, ffprobe, and license files from every input in args
// and writes them to args.Output as an xz compressed tar archive. Files are
// matched by base name, ignoring case, and are stored at the root of the
// archive under their lower-cased base name. args.Output is only created or
// replaced when the whole bundle was written. It returns a *common.ArgError
// when args is incomplete or args.Output does not end in .tar.xz.
func Run(args Args) error {
	ctx := context.Background()
	slog.InfoContext(ctx, "Bundling ffmpeg", "architecture", args.Architecture, "operating_system", args.OperatingSystem, "input", args.Input, "output", args.Output, "hash", args.Hash)

	if err := validateArgs(args); err != nil {
		return err
	}

	candidates := map[string]any{
		"ffmpeg":      nil,
		"ffmpeg.exe":  nil,
		"ffprobe":     nil,
		"ffprobe.exe": nil,
		"license":     nil,
		"license.txt": nil,
		"license.md":  nil,
	}
	// Detect the ffmpeg, ffprobe, and license files and create a file for them.
	files := []archives.FileInfo{}

	for _, inputPath := range args.Input {
		u, err := url.Parse(inputPath)
		if err != nil {
			return err
		}

		if args.Hash != "" {
			q := u.Query()
			q.Set("ufs.checksum", args.Hash)
			u.RawQuery = q.Encode()
		}

		slog.InfoContext(ctx, "mounting", "uri", u.String())
		fsys, err := ufs.New(ctx, u.String())
		if err != nil {
			return err
		}
		defer func() {
			if err := fsys.Close(); err != nil {
				slog.ErrorContext(ctx, "ERROR", "error", err)
			}
		}()

		if err := ufs.Walk(fsys, ".", ufs.WalkArgs{}, func(name string) error {
			fstat, err := fsys.Stat(name)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			baseName := strings.ToLower(filepath.Base(name))
			if _, ok := candidates[baseName]; ok {
				files = append(files, archives.FileInfo{
					FileInfo:      fstat,
					NameInArchive: baseName,
					Open: func() (fs.File, error) {
						return fsys.Open(name)
					},
				})
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return writeBundle(ctx, args.Output, files)
}

// writeBundle archives files into an xz compressed tar archive at output. The
// archive is written to a temporary file next to output and renamed into place
// once it is complete, so a failure never leaves a partial bundle behind or
// clobbers an existing one.
func writeBundle(ctx context.Context, output string, files []archives.FileInfo) (err error) {
	f, err := os.CreateTemp(filepath.Dir(output), filepath.Base(output)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		// Close fails when the file was already closed, the error that
		// matters is the one being returned.
		_ = f.Close() //nolint:errcheck
		if removeErr := os.Remove(f.Name()); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			slog.ErrorContext(ctx, "ERROR", "error", removeErr)
		}
	}()

	if err := common.TarXz().Archive(ctx, f, files); err != nil {
		return err
	}
	if err := f.Chmod(outputFileMode); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(f.Name(), output)
}

// validateArgs reports the first required argument that is missing from args.
func validateArgs(args Args) error {
	if args.Architecture == "" {
		return &common.ArgError{Arg: "arch", Message: "Architecture is required"}
	}
	if args.OperatingSystem == "" {
		return &common.ArgError{Arg: "os", Message: "Operating System is required"}
	}
	if len(args.Input) == 0 {
		return &common.ArgError{Arg: "url", Message: "Input is required"}
	}
	if args.Output == "" {
		return &common.ArgError{Arg: "output", Message: "Output path is required"}
	}
	outputName := strings.ToLower(filepath.Base(args.Output))
	if !strings.HasSuffix(outputName, outputExtension) || outputName == outputExtension {
		return &common.ArgError{Arg: "output", Message: "Output path must be a " + outputExtension + " file"}
	}
	return nil
}
