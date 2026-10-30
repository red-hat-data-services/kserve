/*
Copyright 2026 The KServe Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"strings"
	"testing"
)

func TestShortHash(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "sql/v2", "sql-v2", strings.Repeat("x", 512)} {
		got := ShortHash(in)
		if len(got) != 8 {
			t.Errorf("ShortHash(%q) = %q, want 8 characters", in, got)
		}
		if got != ShortHash(in) {
			t.Errorf("ShortHash(%q) is not stable", in)
		}
		if strings.Trim(got, "0123456789abcdef") != "" {
			t.Errorf("ShortHash(%q) = %q, want lowercase hex", in, got)
		}
	}

	if a, b := ShortHash("sql/v2"), ShortHash("sql-v2"); a == b {
		t.Errorf("distinct inputs share a digest: %q", a)
	}
}
