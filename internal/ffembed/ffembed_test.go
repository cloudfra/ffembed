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
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudfra/ffembed/internal/common"
	"github.com/mholt/archives"
)

// zipOf returns a zip archive holding files, keyed by slash separated path.
func zipOf(tb testing.TB, files map[string]string) []byte {
	tb.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			tb.Fatalf("Create(%q) failed, %s", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			tb.Fatalf("Write(%q) failed, %s", name, err)
		}
	}
	if err := w.Close(); err != nil {
		tb.Fatalf("Close() failed, %s", err)
	}
	return buf.Bytes()
}

// bundleOf returns a bundle like the ones ffbundle writes holding files.
func bundleOf(tb testing.TB, files map[string]string) []byte {
	tb.Helper()

	dir := tb.TempDir()
	infos := make([]archives.FileInfo, 0, len(files))
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			tb.Fatalf("WriteFile(%q) failed, %s", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			tb.Fatalf("Stat(%q) failed, %s", path, err)
		}
		infos = append(infos, archives.FileInfo{
			FileInfo:      info,
			NameInArchive: name,
			Open: func() (fs.File, error) {
				return os.Open(filepath.Clean(path))
			},
		})
	}
	var buf bytes.Buffer
	if err := common.TarXz().Archive(tb.Context(), &buf, infos); err != nil {
		tb.Fatalf("Archive() failed, %s", err)
	}
	return buf.Bytes()
}

// checksumOf returns the sha256 checksum of data in the form of the manifest.
func checksumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serve returns the URL of a server that serves files by path.
func serve(tb testing.TB, files map[string][]byte) string {
	tb.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(content); err != nil {
			tb.Errorf("Write(%q) failed, %s", r.URL.Path, err)
		}
	}))
	tb.Cleanup(server.Close)
	return server.URL
}

// assertFiles fails the test unless dir holds exactly the files of want.
func assertFiles(tb testing.TB, dir string, want map[string]string) {
	tb.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatalf("ReadDir(%q) failed, %s", dir, err)
	}
	got := map[string]string{}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Clean(filepath.Join(dir, entry.Name())))
		if err != nil {
			tb.Fatalf("ReadFile(%q) failed, %s", entry.Name(), err)
		}
		got[entry.Name()] = string(content)
	}
	if len(got) != len(want) {
		tb.Fatalf("%q holds %v, want %v", dir, got, want)
	}
	for name, content := range want {
		if got[name] != content {
			tb.Errorf("%q holds %v, want %v", dir, got, want)
		}
	}
}

func TestFfmpegPlaceholderIsNotEmbedded(t *testing.T) {
	embedded := ffmpegEmbedded
	t.Cleanup(func() { ffmpegEmbedded = embedded })

	for _, placeholder := range [][]byte{nil, {}, []byte("a")} {
		ffmpegEmbedded = placeholder
		if _, err := Ffmpeg(); !errors.Is(err, ErrNotEmbedded) {
			t.Errorf("Ffmpeg() with %q embedded = %v, want %v", placeholder, err, ErrNotEmbedded)
		}
	}

	bundle := bundleOf(t, map[string]string{"ffmpeg": "ffmpeg binary"})
	ffmpegEmbedded = bundle
	got, err := Ffmpeg()
	if err != nil {
		t.Fatalf("Ffmpeg() with a bundle embedded failed, %s", err)
	}
	if !bytes.Equal(got, bundle) {
		t.Errorf("Ffmpeg() returned %d bytes, want the %d bytes embedded", len(got), len(bundle))
	}
}

func TestGetManifest(t *testing.T) {
	manifest, err := GetManifest()
	if err != nil {
		t.Fatalf("GetManifest() failed, %s", err)
	}
	for _, platform := range []string{"linux_amd64", "windows_amd64", "darwin_arm64"} {
		if len(manifest[platform]) == 0 {
			t.Errorf("manifest lists no build for %s", platform)
		}
	}

	for platform, variants := range manifest {
		for variant, pkg := range variants {
			name := platform + "/" + variant
			if pkg.License == "" {
				t.Errorf("%s has no license", name)
			}
			if len(pkg.Files) == 0 {
				t.Errorf("%s has no files", name)
			}
			for _, file := range pkg.Files {
				if !strings.HasPrefix(file.File, "https://") {
					t.Errorf("%s file %q is not an https URL", name, file.File)
				}
				if file.Checksum == "" {
					continue
				}
				if _, _, err := parseChecksum(file.Checksum); err != nil {
					t.Errorf("%s file %q has an invalid checksum, %s", name, file.File, err)
				}
			}
		}
	}
}

func TestManifestSelect(t *testing.T) {
	manifest := Manifest{
		"linux_amd64": {
			"gpl":      {License: "GPL-2.0", Files: []File{{File: "gpl"}}},
			"lgpl":     {License: "LGPL-2.0", Files: []File{{File: "lgpl"}}},
			"lgpl-alt": {License: "LGPL-2.0", Files: []File{{File: "lgpl-alt"}}},
		},
		"plan9_amd64": {},
	}

	testCases := []struct {
		name     string
		platform string
		accept   []string
		// want is the file of the selected build, empty when none is.
		want string
	}{
		{name: "accepted license", platform: "linux_amd64", accept: []string{"GPL-2.0"}, want: "gpl"},
		{name: "license ignores case and whitespace", platform: "linux_amd64", accept: []string{" gpl-2.0 "}, want: "gpl"},
		{name: "first accepted license is preferred", platform: "linux_amd64", accept: []string{"LGPL-2.0", "GPL-2.0"}, want: "lgpl"},
		{name: "unavailable licenses are skipped", platform: "linux_amd64", accept: []string{"MIT", "GPL-2.0"}, want: "gpl"},
		{name: "no license accepted", platform: "linux_amd64"},
		{name: "license not offered", platform: "linux_amd64", accept: []string{"MIT"}},
		{name: "unknown platform", platform: "linux_arm64", accept: []string{"GPL-2.0"}},
		{name: "platform without builds", platform: "plan9_amd64", accept: []string{"GPL-2.0"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pkg, err := manifest.Select(tc.platform, tc.accept)
			if tc.want == "" {
				if err == nil {
					t.Errorf("Select() = %v, want error", pkg)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select() failed, %s", err)
			}
			if got := pkg.Files[0].File; got != tc.want {
				t.Errorf("Select() selected %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseChecksum(t *testing.T) {
	valid := checksumOf([]byte("ffmpeg"))

	testCases := []struct {
		name     string
		checksum string
		wantErr  bool
	}{
		{name: "sha256", checksum: valid},
		{name: "upper case", checksum: strings.ToUpper(valid)},
		{name: "sha512", checksum: "sha512:" + strings.Repeat("ab", 64)},
		{name: "no algorithm", checksum: strings.TrimPrefix(valid, "sha256:"), wantErr: true},
		{name: "unsupported algorithm", checksum: "md5:d41d8cd98f00b204e9800998ecf8427e", wantErr: true},
		{name: "truncated digest", checksum: valid[:len(valid)-2], wantErr: true},
		{name: "digest is not hex", checksum: "sha256:" + strings.Repeat("zz", 32), wantErr: true},
		{name: "empty digest", checksum: "sha256:", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, digest, err := parseChecksum(tc.checksum)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseChecksum(%q) succeeded, want error", tc.checksum)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChecksum(%q) failed, %s", tc.checksum, err)
			}
			if digest != strings.ToLower(digest) {
				t.Errorf("parseChecksum(%q) digest = %q, want lower case", tc.checksum, digest)
			}
		})
	}
}

func TestInstalled(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Installed(dir, "linux"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Installed() of an empty directory = %v, want %v", err, fs.ErrNotExist)
	}

	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile() failed, %s", err)
	}
	if _, _, err := Installed(dir, "linux"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Installed() without ffprobe = %v, want %v", err, fs.ErrNotExist)
	}

	if err := os.Mkdir(filepath.Join(dir, "ffprobe"), 0o700); err != nil {
		t.Fatalf("Mkdir() failed, %s", err)
	}
	if _, _, err := Installed(dir, "linux"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Installed() with a directory as ffprobe = %v, want %v", err, fs.ErrNotExist)
	}

	for _, name := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("WriteFile() failed, %s", err)
		}
	}
	ffmpeg, ffprobe, err := Installed(dir, "windows")
	if err != nil {
		t.Fatalf("Installed() failed, %s", err)
	}
	if want := filepath.Join(dir, "ffmpeg.exe"); ffmpeg != want {
		t.Errorf("Installed() ffmpeg = %q, want %q", ffmpeg, want)
	}
	if want := filepath.Join(dir, "ffprobe.exe"); ffprobe != want {
		t.Errorf("Installed() ffprobe = %q, want %q", ffprobe, want)
	}
}

func TestInstallBundle(t *testing.T) {
	want := map[string]string{
		"ffmpeg":  "ffmpeg binary",
		"ffprobe": "ffprobe binary",
		"license": "license text",
	}
	// The directory is created when it does not exist.
	dir := filepath.Join(t.TempDir(), "nested", "work")

	if err := InstallBundle(t.Context(), bundleOf(t, want), dir); err != nil {
		t.Fatalf("InstallBundle() failed, %s", err)
	}

	assertFiles(t, dir, want)
	if runtime.GOOS == "windows" {
		return
	}
	for name, wantMode := range map[string]fs.FileMode{"ffmpeg": 0o755, "ffprobe": 0o755, "license": 0o644} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Stat(%q) failed, %s", name, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Errorf("mode of %q = %o, want %o", name, got, wantMode)
		}
	}
}

func TestInstallBundleInvalid(t *testing.T) {
	dir := t.TempDir()

	if err := InstallBundle(t.Context(), []byte("not a bundle"), dir); err == nil {
		t.Error("InstallBundle() succeeded, want error")
	}
	assertFiles(t, dir, map[string]string{})
}

func TestInstallPackage(t *testing.T) {
	archive := zipOf(t, map[string]string{
		"ffmpeg-7.1/bin/ffmpeg.exe":  "ffmpeg binary",
		"ffmpeg-7.1/bin/ffplay.exe":  "ffplay binary",
		"ffmpeg-7.1/LICENSE.txt":     "license text",
		"ffmpeg-7.1/doc/ffmpeg.html": "docs",
	})
	// The second file is the bare executable rather than an archive, and
	// shares its name with a file of the first that must not win.
	url := serve(t, map[string][]byte{
		"/a/ffmpeg.zip":  archive,
		"/b/ffprobe.exe": []byte("ffprobe binary"),
		"/c/ffmpeg.exe":  []byte("other ffmpeg binary"),
	})
	pkg := &Package{Files: []File{
		{File: url + "/a/ffmpeg.zip?token=1", Checksum: checksumOf(archive)},
		{File: url + "/b/ffprobe.exe", Checksum: checksumOf([]byte("ffprobe binary"))},
		{File: url + "/c/ffmpeg.exe", Checksum: checksumOf([]byte("other ffmpeg binary"))},
	}}
	dir := t.TempDir()

	if err := InstallPackage(t.Context(), http.DefaultClient, pkg, dir, false); err != nil {
		t.Fatalf("InstallPackage() failed, %s", err)
	}

	assertFiles(t, dir, map[string]string{
		"ffmpeg.exe":  "ffmpeg binary",
		"ffprobe.exe": "ffprobe binary",
		"license.txt": "license text",
	})
}

func TestInstallPackageFailures(t *testing.T) {
	archive := zipOf(t, map[string]string{"ffmpeg": "ffmpeg binary"})
	url := serve(t, map[string][]byte{"/ffmpeg.zip": archive})

	testCases := []struct {
		name            string
		pkg             Package
		allowUnverified bool
	}{
		{name: "no files", pkg: Package{}},
		{name: "checksum mismatch", pkg: Package{Files: []File{{File: url + "/ffmpeg.zip", Checksum: checksumOf([]byte("other"))}}}},
		{name: "invalid checksum", pkg: Package{Files: []File{{File: url + "/ffmpeg.zip", Checksum: "sha256:abc"}}}},
		{name: "missing checksum", pkg: Package{Files: []File{{File: url + "/ffmpeg.zip"}}}},
		{name: "not found", pkg: Package{Files: []File{{File: url + "/missing.zip"}}}, allowUnverified: true},
		{name: "unsupported scheme", pkg: Package{Files: []File{{File: "ftp://example.com/ffmpeg.zip"}}}, allowUnverified: true},
		{name: "no file name", pkg: Package{Files: []File{{File: url}}}, allowUnverified: true},
		{name: "malformed url", pkg: Package{Files: []File{{File: "http://[::1"}}}, allowUnverified: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			if err := InstallPackage(t.Context(), http.DefaultClient, &tc.pkg, dir, tc.allowUnverified); err == nil {
				t.Error("InstallPackage() succeeded, want error")
			}
			assertFiles(t, dir, map[string]string{})
		})
	}
}

func TestInstallPackageUnverified(t *testing.T) {
	archive := zipOf(t, map[string]string{"ffmpeg": "ffmpeg binary"})
	url := serve(t, map[string][]byte{"/ffmpeg.zip": archive})
	pkg := &Package{Files: []File{{File: url + "/ffmpeg.zip"}}}
	dir := t.TempDir()

	if err := InstallPackage(t.Context(), http.DefaultClient, pkg, dir, true); err != nil {
		t.Fatalf("InstallPackage() failed, %s", err)
	}

	assertFiles(t, dir, map[string]string{"ffmpeg": "ffmpeg binary"})
}
