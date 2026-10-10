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
	_ "embed"
	"encoding/json"
	"errors"
)

//go:embed manifest.json
var manifestJSON []byte

func Ffmpeg() ([]byte, error) {
	if len(ffmpegEmbedded) > 0 {
		return ffmpegEmbedded, nil
	}
	return nil, errors.New("embedded ffmpeg is not available")
}

// Manifest that's retrieved from ffmpeg static build hosting.
type Manifest struct {
	// TODO: Populate the fields based on manifest.json.
	// TODO: Fill out the manifest details based on static builds for ffmpeg.
}

func GetManifest() (*Manifest, error) {
	manifest := &Manifest{}
	if err := json.Unmarshal(manifestJSON, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}
