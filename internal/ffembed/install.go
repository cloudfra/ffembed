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
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cloudfra/ufs"
)

const (
	// bundleName is the file name the embedded bundle is staged under, its
	// extension is what makes it mountable as an archive.
	bundleName = "ffmpeg.tar.xz"
	// maxDownloadSize is the largest download that is accepted.
	maxDownloadSize = 1 << 30
	// executableMode and licenseMode are the permissions of installed files.
	executableMode = 0o755
	licenseMode    = 0o644
	dirMode        = 0o755
)

// installable maps the lower-cased base name of every file that is installed
// to whether it is an executable.
var installable = map[string]bool{
	"ffmpeg":      true,
	"ffmpeg.exe":  true,
	"ffprobe":     true,
	"ffprobe.exe": true,
	"license":     false,
	"license.txt": false,
	"license.md":  false,
}

// BinaryName returns the file name of the executable called name on goos.
func BinaryName(name string, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// Installed returns the paths of the ffmpeg and ffprobe executables for goos
// in dir. It returns an error wrapping fs.ErrNotExist when either is missing.
func Installed(dir string, goos string) (ffmpeg string, ffprobe string, err error) {
	ffmpeg = filepath.Join(dir, BinaryName("ffmpeg", goos))
	ffprobe = filepath.Join(dir, BinaryName("ffprobe", goos))
	for _, name := range []string{ffmpeg, ffprobe} {
		info, err := os.Stat(name)
		if err != nil {
			return "", "", err
		}
		if !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("%q is not a file, %w", name, fs.ErrNotExist)
		}
	}
	return ffmpeg, ffprobe, nil
}

// InstallBundle installs the ffmpeg, ffprobe, and license files of bundle, a
// .tar.xz archive written by ffbundle, into dir.
func InstallBundle(ctx context.Context, bundle []byte, dir string) error {
	return withStagingDir(func(staging string) error {
		if err := os.WriteFile(filepath.Join(staging, bundleName), bundle, 0o600); err != nil {
			return err
		}
		return installFrom(ctx, staging, dir)
	})
}

// InstallPackage downloads every file of pkg with client, verifies it against
// its checksum, and installs the ffmpeg, ffprobe, and license files found in
// them into dir. Files may be archives or the bare executables. A file
// without a checksum is rejected unless allowUnverified is set.
func InstallPackage(ctx context.Context, client *http.Client, pkg *Package, dir string, allowUnverified bool) error {
	if len(pkg.Files) == 0 {
		return errors.New("ffmpeg package lists no files")
	}
	return withStagingDir(func(staging string) error {
		for i, file := range pkg.Files {
			if file.Checksum == "" && !allowUnverified {
				return fmt.Errorf("refusing to download %q, it has no checksum", file.File)
			}
			// Every file gets its own directory so equally named downloads
			// do not replace each other.
			fileDir := filepath.Join(staging, fmt.Sprint(i))
			if err := os.Mkdir(fileDir, 0o700); err != nil {
				return err
			}
			if err := download(ctx, client, file, fileDir); err != nil {
				return err
			}
		}
		return installFrom(ctx, staging, dir)
	})
}

// withStagingDir calls f with a temporary directory that is removed afterwards.
func withStagingDir(f func(staging string) error) (err error) {
	staging, err := os.MkdirTemp("", "ffembed-*")
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(staging))
	}()
	return f(staging)
}

// download stores the content of file in dir and verifies its checksum. The
// name of the URL is kept because its extension tells what kind of archive
// the file is.
func download(ctx context.Context, client *http.Client, file File, dir string) (err error) {
	u, err := url.Parse(file.File)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("cannot download %q, only http and https are supported", u.Redacted())
	}
	name := path.Base(u.Path)
	// The name must stay inside dir, also where the path separator is not "/".
	if name == "." || name == "/" || name == ".." || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return fmt.Errorf("cannot download %q, it does not name a file", u.Redacted())
	}

	var h hash.Hash
	var want string
	if file.Checksum != "" {
		if h, want, err = parseChecksum(file.Checksum); err != nil {
			return err
		}
	} else {
		slog.WarnContext(ctx, "downloading without a checksum", "url", u.Redacted())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "downloading ffmpeg", "url", u.Redacted())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %q failed, %s", u.Redacted(), resp.Status)
	}

	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: name was checked to be a file name.
	if err != nil {
		return err
	}
	var w io.Writer = f
	if h != nil {
		w = io.MultiWriter(f, h)
	}
	// One byte more than the limit is requested to tell a file of exactly
	// the limit from one that exceeds it.
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxDownloadSize+1))
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if n > maxDownloadSize {
		return fmt.Errorf("downloading %q failed, it is larger than %d bytes", u.Redacted(), maxDownloadSize)
	}
	if h != nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("checksum of %q is %s, want %s", u.Redacted(), got, want)
		}
	}
	return nil
}

// parseChecksum splits a checksum of the form "<algorithm>:<hex>" into the
// hash that computes it and the lower-cased digest that is expected.
func parseChecksum(checksum string) (hash.Hash, string, error) {
	algorithm, digest, ok := strings.Cut(checksum, ":")
	if !ok {
		return nil, "", fmt.Errorf("checksum %q is not in the form <algorithm>:<hex>", checksum)
	}
	var h hash.Hash
	switch strings.ToLower(algorithm) {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return nil, "", fmt.Errorf("checksum algorithm %q is not supported, use sha256 or sha512", algorithm)
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if raw, err := hex.DecodeString(digest); err != nil || len(raw) != h.Size() {
		return nil, "", fmt.Errorf("checksum %q is not a valid %s digest", checksum, algorithm)
	}
	return h, digest, nil
}

// installFrom copies the installable files found anywhere below source, a
// directory holding archives or loose files, into dir. Files are matched by
// base name ignoring case and the first match of every name wins.
func installFrom(ctx context.Context, source string, dir string) (err error) {
	fsys, err := ufs.New(ctx, source)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, fsys.Close())
	}()

	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}

	installed := map[string]bool{}
	return ufs.Walk(fsys, ".", ufs.WalkArgs{IncludeMountedArchive: true}, func(name string) error {
		baseName := strings.ToLower(path.Base(name))
		executable, ok := installable[baseName]
		if !ok || installed[baseName] {
			return nil
		}
		installed[baseName] = true

		mode := fs.FileMode(licenseMode)
		if executable {
			mode = executableMode
		}
		src, err := fsys.Open(name)
		if err != nil {
			return err
		}
		return errors.Join(writeFile(filepath.Join(dir, baseName), src, mode), src.Close())
	})
}

// writeFile writes the content of src to name with the permissions of mode.
// The content is written to a temporary file that is renamed into place, so
// name is never observed half written, even by a concurrent install.
func writeFile(name string, src io.Reader, mode fs.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(f.Name()))
		}
	}()

	_, err = io.Copy(f, src)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
