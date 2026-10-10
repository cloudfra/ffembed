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

// Package ffembed locates, installs, and runs ffmpeg and ffprobe. The
// executables come from a previous install, the system, a bundle embedded in
// the binary, or a download, in that order.
package ffembed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	internalFfembed "github.com/cloudfra/ffembed/internal/ffembed"
	pb "github.com/cloudfra/ffembed/proto"
)

// workDirName is the directory in the user's cache directory that ffmpeg is
// installed into when Args does not name one.
const workDirName = "ffembed"

// FFMpegPackage is an ffmpeg and ffprobe that are ready to run.
type FFMpegPackage interface {
	// FFMpegBinary returns the path of the ffmpeg executable.
	FFMpegBinary() string
	// FFProbeBinary returns the path of the ffprobe executable.
	FFProbeBinary() string
	// FFMpegVersion returns the first line ffmpeg prints for -version (e.g.
	// "ffmpeg version 7.1 Copyright ...").
	FFMpegVersion() (string, error)
	// FFProbeVersion returns the first line ffprobe prints for -version.
	FFProbeVersion() (string, error)
	// FFMpeg starts ffmpeg. The returned FFMpeg must be waited on.
	FFMpeg(*pb.FfmpegRequest) (FFMpeg, error)
	// FFProbe runs ffprobe to completion.
	FFProbe(*pb.FfprobeRequest) (*pb.FfprobeResponse, error)
}

// FFMpeg is a started ffmpeg.
type FFMpeg interface {
	// GetBinary returns the path of the ffmpeg executable.
	GetBinary() string
	// OnChange sets the function that is called, one call at a time, for
	// every event of ffmpeg. Events are only produced while Wait runs, so
	// none are missed when it is set before Wait is called.
	OnChange(func(*pb.FfmpegEvent))
	// Wait blocks until ffmpeg exits. It returns a *RunError that is
	// ErrCancelled when ffmpeg was stopped by Cancel, ErrFailedToStart when
	// it could not run, and ErrRunFailed when it failed by itself.
	Wait() error
	// Cancel stops ffmpeg. It can be called at any time and more than once,
	// Wait must still be called.
	Cancel()
	// Response returns the result of ffmpeg, nil until Wait has returned.
	Response() *pb.FfmpegResponse
}

// FFProbe is an ffprobe that is ready to run.
type FFProbe interface {
	// GetBinary returns the path of the ffprobe executable.
	GetBinary() string
}

// source holds everything New depends on beyond its arguments, so tests can
// replace it.
type source struct {
	// goos and goarch name the platform ffmpeg is needed for.
	goos, goarch string
	// cacheDir returns the directory the default work directory is created in.
	cacheDir func() (string, error)
	// lookPath finds an executable on the system.
	lookPath func(string) (string, error)
	// embedded returns the bundle embedded in the binary.
	embedded func() ([]byte, error)
	// manifest returns the builds that can be downloaded.
	manifest func() (internalFfembed.Manifest, error)
	// client performs the downloads.
	client *http.Client
}

// New returns an ffmpeg and ffprobe that are ready to run, installing them
// into the work directory when needed. It uses the first of these that is
// available:
//
//  1. An install in the work directory left by an earlier call.
//  2. The ffmpeg and ffprobe on the PATH, when args.prefer_installed is set.
//  3. The bundle embedded in the binary.
//  4. A download of args.remote_url, or when args.allow_download is set, of
//     the build in the built-in manifest with a license in args.accept_license.
//
// Downloads are verified against their checksum. New returns an error
// wrapping ErrUnavailable when none of them is available.
func New(args *pb.Args) (FFMpegPackage, error) {
	return source{
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		cacheDir: os.UserCacheDir,
		lookPath: exec.LookPath,
		embedded: internalFfembed.Ffmpeg,
		manifest: internalFfembed.GetManifest,
		client:   http.DefaultClient,
	}.newPackage(context.Background(), args)
}

func (s source) newPackage(ctx context.Context, args *pb.Args) (FFMpegPackage, error) {
	workDir, err := s.workDir(args)
	if err != nil {
		return nil, err
	}

	// A previous call already installed ffmpeg.
	if pkg, err := s.installed(workDir); err == nil {
		return pkg, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	if args.GetPreferInstalled() {
		if pkg, ok := s.external(); ok {
			return pkg, nil
		}
	}

	bundle, err := s.embedded()
	switch {
	case err == nil:
		if err := internalFfembed.InstallBundle(ctx, bundle, workDir); err != nil {
			return nil, fmt.Errorf("cannot install the embedded ffmpeg, %w", err)
		}
		return s.installedAfter(workDir, "the embedded bundle")
	case !errors.Is(err, internalFfembed.ErrNotEmbedded):
		return nil, err
	}

	switch {
	case args.GetRemoteUrl() != "":
		// The caller chose the file, so its license is theirs to judge and
		// the checksum is optional.
		pkg := &internalFfembed.Package{Files: []internalFfembed.File{{
			File:     args.GetRemoteUrl(),
			Checksum: args.GetRemoteUrlChecksum(),
		}}}
		if err := internalFfembed.InstallPackage(ctx, s.client, pkg, workDir, true); err != nil {
			return nil, fmt.Errorf("cannot install ffmpeg from %q, %w", args.GetRemoteUrl(), err)
		}
		return s.installedAfter(workDir, args.GetRemoteUrl())
	case args.GetAllowDownload():
		manifest, err := s.manifest()
		if err != nil {
			return nil, fmt.Errorf("cannot read the ffmpeg manifest, %w", err)
		}
		pkg, err := manifest.Select(s.goos+"_"+s.goarch, args.GetAcceptLicense())
		if err != nil {
			return nil, fmt.Errorf("%w, %w", ErrUnavailable, err)
		}
		if err := internalFfembed.InstallPackage(ctx, s.client, pkg, workDir, false); err != nil {
			return nil, fmt.Errorf("cannot install ffmpeg from the manifest, %w", err)
		}
		return s.installedAfter(workDir, "the manifest")
	}

	return nil, fmt.Errorf("%w, it is not installed in %q, not embedded for %s/%s, and downloading it is not allowed", ErrUnavailable, workDir, s.goos, s.goarch)
}

// workDir returns the absolute directory ffmpeg is installed into.
func (s source) workDir(args *pb.Args) (string, error) {
	if dir := args.GetWorkDir(); dir != "" {
		return filepath.Abs(dir)
	}
	cacheDir, err := s.cacheDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine the work directory, set work_dir, %w", err)
	}
	return filepath.Join(cacheDir, workDirName, s.goos+"_"+s.goarch), nil
}

// installed returns the ffmpeg that is installed in workDir. It returns an
// error wrapping fs.ErrNotExist when there is none.
func (s source) installed(workDir string) (FFMpegPackage, error) {
	ffmpeg, ffprobe, err := internalFfembed.Installed(workDir, s.goos)
	if err != nil {
		return nil, err
	}
	return &ffmpegPackage{ffmpeg: ffmpeg, ffprobe: ffprobe}, nil
}

// installedAfter returns the ffmpeg in workDir after it was installed from
// what, which must have provided both executables.
func (s source) installedAfter(workDir string, what string) (FFMpegPackage, error) {
	pkg, err := s.installed(workDir)
	if err != nil {
		return nil, fmt.Errorf("%s does not hold both ffmpeg and ffprobe for %s, %w", what, s.goos, err)
	}
	return pkg, nil
}

// external returns the ffmpeg found on the system, if both executables are.
func (s source) external() (FFMpegPackage, bool) {
	ffmpeg, err := s.lookPath(internalFfembed.BinaryName("ffmpeg", s.goos))
	if err != nil {
		return nil, false
	}
	ffprobe, err := s.lookPath(internalFfembed.BinaryName("ffprobe", s.goos))
	if err != nil {
		return nil, false
	}
	return &ffmpegPackage{ffmpeg: ffmpeg, ffprobe: ffprobe}, true
}

// ffmpegPackage is the FFMpegPackage of two executables on disk.
type ffmpegPackage struct {
	ffmpeg  string
	ffprobe string
}

func (p *ffmpegPackage) FFMpegBinary() string {
	return p.ffmpeg
}

func (p *ffmpegPackage) FFProbeBinary() string {
	return p.ffprobe
}

func (p *ffmpegPackage) FFMpegVersion() (string, error) {
	return version("ffmpeg", p.ffmpeg)
}

func (p *ffmpegPackage) FFProbeVersion() (string, error) {
	return version("ffprobe", p.ffprobe)
}

// version returns the first line binary prints for -version.
func version(name string, binary string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), binary, "-version") //nolint:gosec // G204: binary is the ffmpeg New resolved.
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", newRunError(name, binary, err, strings.TrimSpace(stderr.String()), false)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(stdout.String()), "\n")
	return strings.TrimSpace(line), nil
}
