// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package yqutil

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentValidationAndEvaluation(t *testing.T) {
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value := fmt.Sprintf("request-%d", i)
			for range 4 {
				if err := ValidateContent([]byte("source: " + value + "\n")); err != nil {
					t.Error(err)
					return
				}
				out, err := EvaluateExpressionPlain(`.result = .source | del(.source)`, "source: "+value+"\n", false)
				if err != nil || strings.TrimSpace(out) != "result: "+value {
					t.Errorf("evaluation for %s returned %q, %v", value, out, err)
					return
				}
				if err := ValidateContent([]byte("broken: [\n")); err == nil {
					t.Error("malformed YAML was accepted")
					return
				}
			}
		}()
	}
	workers.Wait()
}
