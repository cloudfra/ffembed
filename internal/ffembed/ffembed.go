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

// Package ffembed holds the embedded ffmpeg bundle, the manifest of ffmpeg
// builds that can be downloaded, and the logic that installs either one.
package ffembed

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

//go:embed manifest.json
var manifestJSON []byte

// xzMagic is the header every xz stream starts with. The bundle committed to
// the repository is a placeholder, a real one is written by ffbundle.
var xzMagic = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}

// ErrNotEmbedded is returned by Ffmpeg when the binary was built without an
// ffmpeg bundle for its platform.
var ErrNotEmbedded = errors.New("embedded ffmpeg is not available")

// Ffmpeg returns the embedded ffmpeg bundle, a .tar.xz archive written by
// ffbundle that holds ffmpeg, ffprobe, and their license.
func Ffmpeg() ([]byte, error) {
	if bytes.HasPrefix(ffmpegEmbedded, xzMagic) {
		return ffmpegEmbedded, nil
	}
	return nil, ErrNotEmbedded
}

// Manifest lists the ffmpeg builds that can be downloaded, keyed by platform
// in the form GOOS_GOARCH (e.g. linux_amd64) and then by variant (e.g. gpl).
type Manifest map[string]map[string]Package

// Package is one downloadable build of ffmpeg.
type Package struct {
	// Files that together hold ffmpeg, ffprobe, and their license.
	Files []File `json:"files"`
	// License is the SPDX identifier of the license of the build.
	License string `json:"license"`
}

// File is one file of a Package.
type File struct {
	// File is the URL of an archive or a binary.
	File string `json:"file"`
	// Checksum of the file in the form "sha256:<hex>".
	Checksum string `json:"checksum"`
}

// GetManifest returns the manifest that is built into the binary.
func GetManifest() (Manifest, error) {
	manifest := Manifest{}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Select returns the build for platform whose license is the first of
// acceptLicense that the manifest offers. Licenses are compared ignoring case.
func (m Manifest) Select(platform string, acceptLicense []string) (*Package, error) {
	variants, ok := m[platform]
	if !ok || len(variants) == 0 {
		return nil, fmt.Errorf("no ffmpeg build is listed for %s", platform)
	}
	// Variants are visited in name order so the result is deterministic when
	// more than one of them has the same license.
	names := make([]string, 0, len(variants))
	for name := range variants {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, license := range acceptLicense {
		for _, name := range names {
			if pkg := variants[name]; strings.EqualFold(pkg.License, strings.TrimSpace(license)) {
				return &pkg, nil
			}
		}
	}

	offered := make([]string, 0, len(names))
	for _, name := range names {
		offered = append(offered, variants[name].License)
	}
	return nil, fmt.Errorf("no ffmpeg build for %s has an accepted license, accepted %q, available %q", platform, acceptLicense, offered)
}
