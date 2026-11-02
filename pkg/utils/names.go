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
	"crypto/sha256"
	"fmt"
)

// ShortHash returns exactly 8 lowercase hex characters derived from s.
//
// The 8-character width is a compatibility contract, not an implementation
// detail: unique HTTPRoute rule names carry it, so widening it renames those
// rules. Not for security use: 32 bits.
func ShortHash(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:8]
}
