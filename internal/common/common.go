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

// Package common holds helpers shared by the ffembed commands.
package common

import "strings"

// CSVToList splits a comma separated string into its unique values. Whitespace
// around each value is trimmed and empty values are dropped. The values are
// returned in the order they first appear.
func CSVToList(val string) []string {
	seen := map[string]bool{}
	result := []string{}
	for v := range strings.SplitSeq(val, ",") {
		k := strings.TrimSpace(v)
		if k != "" && !seen[k] {
			seen[k] = true
			result = append(result, k)
		}
	}
	return result
}
