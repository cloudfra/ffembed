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

package common

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestCSVToList(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: []string{}},
		{name: "only separators", input: " , ,,", want: []string{}},
		{name: "single", input: "a", want: []string{"a"}},
		{name: "multiple", input: "a,b,c", want: []string{"a", "b", "c"}},
		{name: "trims whitespace", input: " a ,\tb\n, c d ", want: []string{"a", "b", "c d"}},
		{name: "drops empty values", input: "a,,b,", want: []string{"a", "b"}},
		{name: "removes duplicates", input: "a,b,a, b", want: []string{"a", "b"}},
		{name: "keeps order", input: "c,a,b,a", want: []string{"c", "a", "b"}},
		{name: "url", input: "https://example.com/ffmpeg.tar.xz?a=1,/tmp/ffmpeg", want: []string{"https://example.com/ffmpeg.tar.xz?a=1", "/tmp/ffmpeg"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := CSVToList(tc.input)
			if got == nil {
				t.Fatalf("CSVToList(%q) = nil, want non-nil", tc.input)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("CSVToList(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestArgErrorError(t *testing.T) {
	err := &ArgError{Arg: "output", Message: "Output path is required"}
	want := "invalid argument: output: Output path is required"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestArgErrorAs(t *testing.T) {
	wrapped := fmt.Errorf("run failed: %w", &ArgError{Arg: "arch", Message: "Architecture is required"})

	var argErr *ArgError
	if !errors.As(wrapped, &argErr) {
		t.Fatalf("errors.As(%v) = false, want true", wrapped)
	}
	if argErr.Arg != "arch" {
		t.Errorf("Arg = %q, want %q", argErr.Arg, "arch")
	}
}
